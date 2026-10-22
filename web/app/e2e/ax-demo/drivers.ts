/**
 * How the agent reaches the browser. Each driver turns a run into the extra
 * `claude` CLI arguments (and any files) it needs; the runner does the rest.
 *
 * - `playwright-mcp`: the official Playwright MCP server (`@playwright/mcp`),
 *   headless and isolated, as the only tool source (`--strict-mcp-config`,
 *   built-in tools disabled). The agent sees accessibility-tree snapshots.
 * - `playwright-cli`: the Playwright CLI (`@playwright/cli`, the
 *   `playwright-cli` binary) driven through Bash, which is restricted to that
 *   one command. Same engine and snapshots, delivered as shell output.
 *
 * Package versions are pinned so a run is reproducible; bump deliberately.
 */
import { mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

export const PLAYWRIGHT_MCP_PACKAGE = '@playwright/mcp@0.0.83';
export const PLAYWRIGHT_CLI_PACKAGE = '@playwright/cli@0.1.22';

export type DriverName = 'playwright-mcp' | 'playwright-cli' | 'webmcp' | 'webmcp-soup';
export const DRIVERS: DriverName[] = ['playwright-mcp', 'playwright-cli', 'webmcp', 'webmcp-soup'];

export interface DriverSetup {
  /** Extra arguments for `claude`. */
  args: string[];
  /** Extra environment for the `claude` process. */
  env: Record<string, string>;
  /** Appended to the task prompt (tool-specific usage notes). */
  promptSuffix: string;
  /** Called after the run, best-effort, to release the browser. */
  cleanup?: () => Promise<void> | void;
}

export interface DriverContext {
  /** Per-run scratch directory (MCP config, CLI session data, output files). */
  runDir: string;
  /** Unique per run; used to isolate CLI browser sessions. */
  runId: string;
  /** `--executable-path` for a pre-installed Chromium, when set. */
  browserExecutable?: string;
  /** Launch Chromium without its sandbox (needed when running as root, e.g. in a container). */
  noSandbox?: boolean;
  viewport: { width: number; height: number };
  /** The page the agent starts on; the WebMCP drivers open it themselves. */
  startUrl: string;
}

export function setupDriver(name: DriverName, ctx: DriverContext): DriverSetup {
  mkdirSync(ctx.runDir, { recursive: true });
  switch (name) {
    case 'playwright-mcp':
      return mcpDriver(ctx);
    case 'playwright-cli':
      return cliDriver(ctx);
    case 'webmcp':
      return webmcpDriver(ctx, '1');
    case 'webmcp-soup':
      return webmcpDriver(ctx, 'soup');
  }
}

function mcpDriver(ctx: DriverContext): DriverSetup {
  const serverArgs = [
    '-y',
    PLAYWRIGHT_MCP_PACKAGE,
    '--headless',
    '--isolated',
    `--viewport-size=${ctx.viewport.width}x${ctx.viewport.height}`,
    `--output-dir=${join(ctx.runDir, 'mcp-output')}`,
  ];
  if (ctx.browserExecutable) serverArgs.push(`--executable-path=${ctx.browserExecutable}`);
  if (ctx.noSandbox) serverArgs.push('--no-sandbox');
  // AX_DEMO_VIDEO=1 records the browser to mcp-output/ through the MCP server's saveVideo config.
  if (process.env['AX_DEMO_VIDEO']) {
    const mcpConfig = join(ctx.runDir, 'playwright-mcp.json');
    writeFileSync(
      mcpConfig,
      JSON.stringify({ browser: { contextOptions: { recordVideo: { dir: join(ctx.runDir, 'video'), size: ctx.viewport } } } }),
    );
    serverArgs.push(`--config=${mcpConfig}`);
  }
  const configPath = join(ctx.runDir, 'mcp.json');
  writeFileSync(configPath, JSON.stringify({ mcpServers: { playwright: { command: 'npx', args: serverArgs } } }, null, 2));
  return {
    args: [
      '--mcp-config',
      configPath,
      '--strict-mcp-config',
      // No built-in tools at all: the browser is the only way to act.
      '--tools',
      '',
      '--allowedTools',
      'mcp__playwright',
    ],
    env: {},
    promptSuffix: '',
  };
}

function cliDriver(ctx: DriverContext): DriverSetup {
  const session = `axdemo-${ctx.runId}`;
  const cli = `npx -y ${PLAYWRIGHT_CLI_PACKAGE}`;
  // The CLI reads its browser settings from the PLAYWRIGHT_MCP_* environment (headless by default).
  const env: Record<string, string> = {
    PLAYWRIGHT_CLI_SESSION: session,
    PLAYWRIGHT_MCP_VIEWPORT_SIZE: `${ctx.viewport.width}x${ctx.viewport.height}`,
    PLAYWRIGHT_MCP_OUTPUT_DIR: join(ctx.runDir, 'cli-output'),
  };
  if (ctx.browserExecutable) env['PLAYWRIGHT_MCP_EXECUTABLE_PATH'] = ctx.browserExecutable;
  if (ctx.noSandbox) env['PLAYWRIGHT_MCP_SANDBOX'] = 'false';
  return {
    args: [
      // Bash only, and only for playwright-cli. The agent cannot curl the API or read files.
      '--tools',
      'Bash',
      '--allowedTools',
      'Bash(npx -y @playwright/cli*)',
      'Bash(playwright-cli *)',
    ],
    env,
    promptSuffix: [
      '',
      'Your browser is the Playwright CLI, run through Bash. Invoke it as:',
      `  ${cli} <command> [args]`,
      `Start with: ${cli} open <url>`,
      `Useful commands: snapshot, click <ref>, fill <ref> "<text>", check <ref>, select <ref> "<value>", press <key>, screenshot, find "<text>".`,
      'Run snapshot to read the page; open and other commands only save it to a file you cannot read.',
      'Element refs (e.g. e15) come from the most recent snapshot.',
    ].join('\n'),
    cleanup: async () => {
      const { spawn } = await import('node:child_process');
      await new Promise<void>(resolve => {
        const child = spawn('npx', ['-y', PLAYWRIGHT_CLI_PACKAGE, 'close'], {
          env: { ...process.env, ...env },
          cwd: ctx.runDir,
          stdio: 'ignore',
        });
        child.on('close', () => resolve());
        child.on('error', () => resolve());
      });
    },
  };
}

/**
 * WebMCP: the app registers its own tools with navigator.modelContext (`?webmcp=1`, or `soup` for
 * the undescribed variant) and webmcp-server.mjs hands exactly those tools to the agent. No browser
 * tools at all, so the page's tools are its only interface. The ax level still applies to the
 * markup, which the agent never reads, so every tier should look the same through this driver.
 */
function webmcpDriver(ctx: DriverContext, mode: '1' | 'soup'): DriverSetup {
  const url = new URL(ctx.startUrl);
  url.searchParams.set('webmcp', mode);
  const env: Record<string, string> = { WEBMCP_URL: url.toString(), WEBMCP_VIEWPORT: `${ctx.viewport.width}x${ctx.viewport.height}` };
  if (ctx.browserExecutable) env['WEBMCP_EXECUTABLE'] = ctx.browserExecutable;
  if (process.env['AX_DEMO_VIDEO']) env['WEBMCP_VIDEO_DIR'] = join(ctx.runDir, 'video');
  const configPath = join(ctx.runDir, 'mcp.json');
  const server = join(process.cwd(), 'e2e', 'ax-demo', 'webmcp-server.mjs');
  writeFileSync(configPath, JSON.stringify({ mcpServers: { webmcp: { command: 'node', args: [server], env } } }, null, 2));
  return {
    args: ['--mcp-config', configPath, '--strict-mcp-config', '--tools', '', '--allowedTools', 'mcp__webmcp'],
    env: {},
    promptSuffix: [
      '',
      'The app is already open in a browser. Your tools are the actions the app itself exposes through WebMCP;',
      'there are no click, type or screenshot tools.',
    ].join('\n'),
  };
}
