/**
 * Aggregates a results CSV from run.ts into per-level (and per-level x driver)
 * numbers, and joins in the eval-side signals when their JSON is present.
 *
 *   npx tsx e2e/ax-demo/summarize.ts [--in results.csv] [--json summary.json]
 *
 * Metrics (see README "Metrics"):
 *   success_rate        passes / runs
 *   mean_tool_calls     over all runs
 *   tokens_per_success  (input + output + cache tokens over ALL runs) / passes — the cost of one
 *                       successful task, failures included; Infinity when nothing passed
 *   claim_accuracy      runs whose done-claim matched the grade / runs
 */
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve, sep } from 'node:path';
import { parseArgs } from 'node:util';

export interface Aggregate {
  level: number;
  driver: string;
  runs: number;
  passes: number;
  successRate: number;
  meanToolCalls: number;
  meanBrowserToolCalls: number;
  meanScreenshots: number;
  meanWallMs: number;
  totalTokens: number;
  tokensPerSuccess: number;
  claimAccuracy: number;
  falseDoneClaims: number;
  costPerSuccessUsd: number;
}

type CsvRow = Record<string, string>;

/** Minimal RFC 4180 reader (quoted cells, doubled quotes, embedded commas/newlines). */
export function parseCsv(text: string): CsvRow[] {
  const records: string[][] = [];
  let field = '';
  let record: string[] = [];
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const char = text[i];
    if (quoted) {
      if (char === '"' && text[i + 1] === '"') {
        field += '"';
        i++;
      } else if (char === '"') {
        quoted = false;
      } else {
        field += char;
      }
    } else if (char === '"') {
      quoted = true;
    } else if (char === ',') {
      record.push(field);
      field = '';
    } else if (char === '\n') {
      record.push(field);
      records.push(record);
      record = [];
      field = '';
    } else if (char !== '\r') {
      field += char;
    }
  }
  if (field || record.length) {
    record.push(field);
    records.push(record);
  }
  const [header, ...rows] = records;
  if (!header) return [];
  return rows.filter(row => row.length === header.length).map(row => Object.fromEntries(header.map((name, i) => [name, row[i]])));
}

export function aggregate(rows: CsvRow[], by: (row: CsvRow) => string): Aggregate[] {
  const groups = new Map<string, CsvRow[]>();
  for (const row of rows) {
    const key = by(row);
    groups.set(key, [...(groups.get(key) ?? []), row]);
  }
  return [...groups.entries()]
    .map(([key, group]) => {
      const num = (row: CsvRow, column: string): number => Number(row[column] ?? 0) || 0;
      const passes = group.filter(row => row['pass'] === 'true').length;
      const totalTokens = group.reduce(
        (sum, row) => sum + num(row, 'input_tokens') + num(row, 'output_tokens') + num(row, 'cache_read_tokens') + num(row, 'cache_creation_tokens'),
        0,
      );
      const mean = (column: string): number => group.reduce((sum, row) => sum + num(row, column), 0) / group.length;
      const [level, driver = 'all'] = key.split('|');
      return {
        level: Number(level),
        driver,
        runs: group.length,
        passes,
        successRate: passes / group.length,
        meanToolCalls: mean('tool_calls'),
        meanBrowserToolCalls: mean('browser_tool_calls'),
        meanScreenshots: mean('screenshots'),
        meanWallMs: mean('wall_ms'),
        totalTokens,
        tokensPerSuccess: passes > 0 ? totalTokens / passes : Number.POSITIVE_INFINITY,
        claimAccuracy: group.filter(row => row['claim_correct'] === 'true').length / group.length,
        falseDoneClaims: group.filter(row => row['claimed_done'] === 'true' && row['pass'] !== 'true').length,
        costPerSuccessUsd: passes > 0 ? group.reduce((sum, row) => sum + num(row, 'cost_usd'), 0) / passes : Number.POSITIVE_INFINITY,
      };
    })
    .sort((a, b) => a.level - b.level || a.driver.localeCompare(b.driver));
}

function fmt(value: number, digits = 1): string {
  if (!Number.isFinite(value)) return '—';
  return value >= 10_000 ? Math.round(value).toLocaleString('en-US') : value.toFixed(digits);
}

function table(title: string, aggregates: Aggregate[], evals: EvalSignals): string {
  const header = [
    'level',
    'driver',
    'runs',
    'success',
    'mean tools',
    'mean screenshots',
    'tokens / success',
    '$ / success',
    'false "done"',
    'axe violations',
    'getByRole pass',
  ];
  const lines = aggregates.map(a =>
    [
      String(a.level),
      a.driver,
      String(a.runs),
      `${fmt(a.successRate * 100, 0)}%`,
      fmt(a.meanToolCalls),
      fmt(a.meanScreenshots),
      fmt(a.tokensPerSuccess, 0),
      fmt(a.costPerSuccessUsd, 3),
      String(a.falseDoneClaims),
      evals.axe[a.level] === undefined ? 'n/a' : String(evals.axe[a.level]),
      evals.functional[a.level] === undefined ? 'n/a' : `${fmt(evals.functional[a.level] * 100, 0)}%`,
    ].join(' | '),
  );
  return [`## ${title}`, '', `| ${header.join(' | ')} |`, `| ${header.map(() => '---').join(' | ')} |`, ...lines.map(line => `| ${line} |`), ''].join(
    '\n',
  );
}

interface EvalSignals {
  /** Total axe violations (nodes) per level, summed over the scanned states. */
  axe: Record<number, number>;
  /** Functional memory-spec pass rate per level. */
  functional: Record<number, number>;
}

function readEvals(dir: string): EvalSignals {
  const signals: EvalSignals = { axe: {}, functional: {} };
  const axePath = resolve(dir, 'axe-by-level.json');
  if (existsSync(axePath)) {
    const report = JSON.parse(readFileSync(axePath, 'utf8')) as { levels: Record<string, { totalViolationNodes: number }> };
    for (const [level, entry] of Object.entries(report.levels ?? {})) signals.axe[Number(level)] = entry.totalViolationNodes;
  }
  const functionalPath = resolve(dir, 'functional-by-level.json');
  if (existsSync(functionalPath)) {
    const report = JSON.parse(readFileSync(functionalPath, 'utf8')) as { levels: Record<string, { passRate: number }> };
    for (const [level, entry] of Object.entries(report.levels ?? {})) signals.functional[Number(level)] = entry.passRate;
  }
  return signals;
}

function main(): void {
  const { values } = parseArgs({
    options: {
      in: { type: 'string', default: 'e2e/ax-demo/results/results.csv' },
      evals: { type: 'string', default: 'e2e/ax-demo/results' },
      json: { type: 'string' },
    },
  });
  const rows = parseCsv(readFileSync(resolve(values.in), 'utf8'));
  if (rows.length === 0) throw new Error(`no rows in ${values.in}`);
  const evals = readEvals(values.evals);
  const byLevel = aggregate(rows, row => row['level']);
  const byLevelDriver = aggregate(rows, row => `${row['level']}|${row['driver']}`);
  const byLevelTask = aggregate(rows, row => `${row['level']}|${row['task']}`);

  process.stdout.write(
    [
      table('Per level (all drivers, all tasks)', byLevel, evals),
      table('Per level x driver', byLevelDriver, evals),
      table('Per level x task (driver column shows the task)', byLevelTask, evals),
    ].join('\n'),
  );
  if (values.json) {
    writeFileSync(resolve(values.json), JSON.stringify({ byLevel, byLevelDriver, byLevelTask, evals }, null, 2));
  }
}

if (process.argv[1] && resolve(process.argv[1]).endsWith(`${sep}ax-demo${sep}summarize.ts`)) {
  main();
}
