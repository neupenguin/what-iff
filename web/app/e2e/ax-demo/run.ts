/**
 * Runs the agent-experience matrix: level x driver x task x N runs. Each run
 * seeds a fresh account, starts `claude -p` on the task with the start URL
 * pinned to `?ax=<level>`, parses its stream-json transcript, grades the
 * result through the API, and appends one CSV row.
 *
 *   npx tsx e2e/ax-demo/run.ts --levels 0,1,2,3 --drivers playwright-mcp --tasks T1,T2 --runs 3
 *
 * Requires the API (`make run-mock`) and the app (`make web`) to be up, and a
 * `claude` CLI that is signed in. See e2e/ax-demo/README.md.
 */
import { spawn } from 'node:child_process';
import { appendFileSync, existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';

import { DRIVERS, setupDriver, type DriverName } from './drivers';
import { seed, type SeedResult } from './seed';
import { parseStreamJson, type RunMetrics } from './stream-metrics';
import { TASKS, buildPrompt, claimedDone, grade, taskById, type TaskDefinition } from './tasks';

export const CSV_COLUMNS = [
  'timestamp',
  'level',
  'driver',
  'task',
  'run',
  'pass',
  'claimed_done',
  'claim_correct',
  'tool_calls',
  'browser_tool_calls',
  'input_tokens',
  'output_tokens',
  'cache_read_tokens',
  'cache_creation_tokens',
  'wall_ms',
  'screenshots',
  'turns',
  'cost_usd',
  'exit_code',
  'timed_out',
  'fail_reasons',
  'transcript',
] as const;

type Row = Record<(typeof CSV_COLUMNS)[number], string | number | boolean>;

interface Options {
  levels: number[];
  drivers: DriverName[];
  tasks: TaskDefinition[];
  runs: number;
  appUrl: string;
  out: string;
  artifacts: string;
  model?: string;
  timeoutMs: number;
  maxBudgetUsd?: string;
  browserExecutable?: string;
  noSandbox: boolean;
  dryRun: boolean;
}

function parseOptions(): Options {
  const { values } = parseArgs({
    options: {
      levels: { type: 'string', default: '0,1,2,3' },
      drivers: { type: 'string', default: DRIVERS.join(',') },
      tasks: { type: 'string', default: TASKS.map(task => task.id).join(',') },
      runs: { type: 'string', default: '3' },
      'app-url': { type: 'string', default: process.env['E2E_BASE_URL'] ?? 'http://localhost:4200' },
      out: { type: 'string', default: 'e2e/ax-demo/results/results.csv' },
      artifacts: { type: 'string', default: 'e2e/ax-demo/results/runs' },
      model: { type: 'string' },
      'timeout-min': { type: 'string', default: '10' },
      'max-budget-usd': { type: 'string' },
      'browser-executable': { type: 'string', default: process.env['AX_DEMO_BROWSER'] },
      'no-sandbox': { type: 'boolean', default: process.env['AX_DEMO_NO_SANDBOX'] === '1' },
      'dry-run': { type: 'boolean', default: false },
    },
  });
  const levels = values.levels.split(',').map(level => Number.parseInt(level, 10));
  if (levels.some(level => !(level >= 0 && level <= 3))) throw new Error(`--levels must be 0-3, got ${values.levels}`);
  const drivers = values.drivers.split(',') as DriverName[];
  for (const driver of drivers) {
    if (!DRIVERS.includes(driver)) throw new Error(`unknown driver ${driver} (expected ${DRIVERS.join(', ')})`);
  }
  return {
    levels,
    drivers,
    tasks: values.tasks.split(',').map(taskById),
    runs: Number.parseInt(values.runs, 10),
    appUrl: values['app-url'].replace(/\/$/, ''),
    out: resolve(values.out),
    artifacts: resolve(values.artifacts),
    model: values.model,
    timeoutMs: Number.parseFloat(values['timeout-min']) * 60_000,
    maxBudgetUsd: values['max-budget-usd'],
    browserExecutable: values['browser-executable'],
    noSandbox: values['no-sandbox'],
    dryRun: values['dry-run'],
  };
}

/** The URL the agent is sent to. `?ax=` is read before the login redirect and survives it (AxLevelService). */
export function startUrl(appUrl: string, level: number): string {
  return `${appUrl}/memories?ax=${level}`;
}

interface ClaudeRun {
  transcript: string;
  exitCode: number | null;
  timedOut: boolean;
  wallMs: number;
}

function runClaude(prompt: string, args: string[], env: Record<string, string>, cwd: string, timeoutMs: number): Promise<ClaudeRun> {
  return new Promise(resolvePromise => {
    const started = Date.now();
    const child = spawn('claude', ['-p', prompt, '--output-format', 'stream-json', '--verbose', ...args], {
      cwd,
      env: { ...process.env, ...env },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let transcript = '';
    let timedOut = false;
    child.stdout.on('data', (chunk: Buffer) => (transcript += chunk.toString()));
    child.stderr.on('data', (chunk: Buffer) => process.stderr.write(chunk));
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill('SIGTERM');
    }, timeoutMs);
    child.on('close', code => {
      clearTimeout(timer);
      resolvePromise({ transcript, exitCode: code, timedOut, wallMs: Date.now() - started });
    });
    child.on('error', err => {
      clearTimeout(timer);
      transcript += `\n${JSON.stringify({ type: 'runner_error', message: String(err) })}`;
      resolvePromise({ transcript, exitCode: null, timedOut, wallMs: Date.now() - started });
    });
  });
}

function csvCell(value: string | number | boolean): string {
  const text = String(value);
  return /[",\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

export function appendRow(path: string, row: Row): void {
  if (!existsSync(path)) {
    mkdirSync(resolve(path, '..'), { recursive: true });
    writeFileSync(path, `${CSV_COLUMNS.join(',')}\n`);
  }
  appendFileSync(path, `${CSV_COLUMNS.map(column => csvCell(row[column])).join(',')}\n`);
}

async function runOne(opts: Options, level: number, driver: DriverName, task: TaskDefinition, run: number): Promise<Row> {
  const runId = `L${level}-${driver}-${task.id}-r${run}-${Date.now().toString(36)}`;
  const runDir = join(opts.artifacts, runId);
  mkdirSync(runDir, { recursive: true });

  const seeded: SeedResult = await seed();
  writeFileSync(join(runDir, 'seed.json'), JSON.stringify(seeded, null, 2));

  const setup = setupDriver(driver, {
    runDir,
    runId,
    browserExecutable: opts.browserExecutable,
    noSandbox: opts.noSandbox,
    viewport: { width: 1280, height: 900 },
  });
  const prompt = buildPrompt(task, startUrl(opts.appUrl, level), seeded) + setup.promptSuffix;
  writeFileSync(join(runDir, 'prompt.txt'), prompt);

  // --no-chrome and --disable-slash-commands keep browser integrations and skills from the
  // operator's own setup out of the run, so the driver's tools are the only way to the page.
  const args = [...setup.args, '--no-chrome', '--disable-slash-commands', '--permission-mode', 'dontAsk', '--no-session-persistence'];
  if (opts.model) args.push('--model', opts.model);
  if (opts.maxBudgetUsd) args.push('--max-budget-usd', opts.maxBudgetUsd);

  let result: ClaudeRun;
  if (opts.dryRun) {
    writeFileSync(join(runDir, 'command.json'), JSON.stringify({ cmd: 'claude', args: ['-p', '<prompt.txt>', ...args], env: setup.env }, null, 2));
    result = { transcript: '', exitCode: null, timedOut: false, wallMs: 0 };
  } else {
    try {
      result = await runClaude(prompt, args, setup.env, runDir, opts.timeoutMs);
    } finally {
      await setup.cleanup?.();
    }
  }
  const transcriptPath = join(runDir, 'transcript.jsonl');
  writeFileSync(transcriptPath, result.transcript);

  const metrics: RunMetrics = parseStreamJson(result.transcript);
  const graded = await grade(task, seeded);
  const claimed = claimedDone(metrics.finalText);
  writeFileSync(join(runDir, 'grade.json'), JSON.stringify({ ...graded, claimed, metrics }, null, 2));

  return {
    timestamp: new Date().toISOString(),
    level,
    driver,
    task: task.id,
    run,
    pass: graded.pass,
    claimed_done: claimed,
    // "Claimed done correctly": the claim agrees with the grade (claimed and passed, or neither).
    claim_correct: claimed === graded.pass,
    tool_calls: metrics.toolCalls,
    browser_tool_calls: metrics.browserToolCalls,
    input_tokens: metrics.inputTokens,
    output_tokens: metrics.outputTokens,
    cache_read_tokens: metrics.cacheReadTokens,
    cache_creation_tokens: metrics.cacheCreationTokens,
    wall_ms: result.wallMs,
    screenshots: metrics.screenshots,
    turns: metrics.turns,
    cost_usd: metrics.costUsd,
    exit_code: result.exitCode ?? '',
    timed_out: result.timedOut,
    fail_reasons: graded.reasons.join(' | '),
    transcript: transcriptPath,
  };
}

async function main(): Promise<void> {
  const opts = parseOptions();
  const total = opts.levels.length * opts.drivers.length * opts.tasks.length * opts.runs;
  let done = 0;
  // Interleave levels inside each run index so drift over a long session (model load, rate limits)
  // spreads across levels instead of landing on whichever level ran last.
  for (let run = 1; run <= opts.runs; run++) {
    for (const task of opts.tasks) {
      for (const driver of opts.drivers) {
        for (const level of opts.levels) {
          done++;
          const label = `[${done}/${total}] level=${level} driver=${driver} task=${task.id} run=${run}`;
          process.stderr.write(`${label} ...\n`);
          const row = await runOne(opts, level, driver, task, run);
          appendRow(opts.out, row);
          process.stderr.write(
            `${label} pass=${row.pass} claimed=${row.claimed_done} tools=${row.tool_calls} in=${row.input_tokens} out=${row.output_tokens} ${row.wall_ms}ms\n`,
          );
        }
      }
    }
  }
  process.stderr.write(`results: ${opts.out}\n`);
}

main().catch((err: unknown) => {
  console.error(err);
  process.exit(1);
});
