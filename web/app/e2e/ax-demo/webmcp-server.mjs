#!/usr/bin/env node
/**
 * A stdio MCP server that hands a page's WebMCP tools to an agent.
 *
 * It opens WEBMCP_URL in headless Chromium with a `navigator.modelContext` bridge installed, waits
 * for the page to register its tools, and exposes exactly those tools over MCP: tools/list returns
 * the page's registrations and tools/call runs `execute()` inside the page. The agent gets no
 * browser tools (no navigate, click or snapshot), so the page's tools are its only interface.
 *
 * The bridge is adapted from WindTunnel's WebMCP arm (github.com/nekuda-ai/WindTunnel,
 * arms/wm-claude.mjs, Apache-2.0), so a what-iff run here and a WindTunnel run see tools the same way.
 *
 * Env: WEBMCP_URL (required), WEBMCP_VIEWPORT=1280x900, WEBMCP_EXECUTABLE (Chromium path),
 * WEBMCP_VIDEO_DIR (record the page as timed JPEG frames), WEBMCP_TIMEOUT_MS=30000 (wait for tools to register).
 *
 * Recording mode (WEBMCP_VIDEO_DIR): tools call the app's services, not its UI, so the page would sit
 * still and the video would be a single frame. To make the run watchable, the page carries a log of
 * the tool calls and a ticking clock (which also keeps frames coming), and after each call the
 * server reloads WEBMCP_URL so the UI shows the change. None of this happens without a video dir.
 *
 * Frames, not Playwright's recordVideo: the agent CLI kills this server when the run ends, before
 * Playwright can finish the webm, which leaves it truncated. A JPEG written every 200ms, named for
 * its milliseconds since start, is complete the moment it lands.
 */
import { chromium } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { createInterface } from "node:readline";

/** Runs in the page before any app script. From WindTunnel's installModelContextBridge. */
function installModelContextBridge() {
  const registrations = new Map();
  let nextId = 0;
  const bridge = {
    registerTool(definition, options = {}) {
      const signal = options.signal;
      if (signal?.aborted) return Promise.reject(new DOMException("Aborted", "AbortError"));
      const id = ++nextId;
      registrations.set(definition.name, { id, definition });
      return new Promise((_, reject) =>
        signal?.addEventListener(
          "abort",
          () => {
            if (registrations.get(definition.name)?.id === id) registrations.delete(definition.name);
            reject(new DOMException("Aborted", "AbortError"));
          },
          { once: true },
        ),
      );
    },
    list: () =>
      [...registrations.values()].map(({ definition }) => ({
        name: definition.name,
        description: definition.description ?? "",
        inputSchema: definition.inputSchema ?? { type: "object", properties: {} },
      })),
    execute: async (name, args) => {
      const tool = registrations.get(name)?.definition;
      if (!tool) throw new Error(`tool "${name}" is not available`);
      return tool.execute(args ?? {});
    },
  };
  Object.defineProperty(globalThis, "__wtModelContextBridge", { configurable: true, value: bridge });
  for (const target of [document, navigator]) {
    try {
      Object.defineProperty(target, "modelContext", { configurable: true, value: bridge });
    } catch {
      target.modelContext = bridge;
    }
  }
}

/** Recording mode only: a fixed panel listing the tool calls so far, kept across reloads. */
function installCallLog() {
  const KEY = 'webmcp.callLog';
  const start = Number(sessionStorage.getItem(KEY + '.t0') || Date.now());
  sessionStorage.setItem(KEY + '.t0', String(start));
  const esc = s => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;');
  function render() {
    let panel = document.getElementById('webmcp-call-log');
    if (!panel) {
      if (!document.body) return;
      panel = document.createElement('div');
      panel.id = 'webmcp-call-log';
      panel.setAttribute('aria-hidden', 'true');
      panel.style.cssText = 'position:fixed;right:16px;bottom:16px;width:470px;max-height:60vh;overflow:hidden;z-index:2147483647;' +
        'background:rgba(20,22,40,.94);color:#e8e9f5;font:12px/1.45 ui-monospace,Menlo,monospace;border-radius:10px;' +
        'padding:12px 14px;box-shadow:0 8px 30px rgba(0,0,0,.35);pointer-events:none';
      document.body.appendChild(panel);
    }
    const calls = JSON.parse(sessionStorage.getItem(KEY) || '[]');
    const secs = ((Date.now() - start) / 1000).toFixed(1);
    panel.innerHTML = `<div style="display:flex;justify-content:space-between;color:#9adcee;margin-bottom:6px"><b>WebMCP tool calls</b><span>${secs}s · ${calls.length} calls</span></div>` +
      calls.slice(-9).map(c => `<div style="margin-top:6px"><span style="color:#00acd6;font-weight:700">${esc(c.name)}</span> ${esc(c.args)}` +
        `<div style="color:${c.error ? '#ff8a7a' : '#8e91b4'};padding-left:12px">→ ${esc(c.result)}</div></div>`).join('');
  }
  setInterval(render, 100);
  globalThis.__webmcpLog = (name, args, result, error) => {
    const calls = JSON.parse(sessionStorage.getItem(KEY) || '[]');
    calls.push({ name, args, result, error });
    sessionStorage.setItem(KEY, JSON.stringify(calls));
    render();
  };
}

/** Short, display-only argument text. Values of a call that signs in, or fails for want of a login, are masked. */
const shortArgs = (args, masked) =>
  Object.entries(args ?? {}).map(([k, v]) => `${k}=${masked ? '•••' : JSON.stringify(v).replace(/([0-9a-f]{8})-[0-9a-f-]{27}/g, '$1…').slice(0, 48)}`).join(' ');
const shortResult = text => text.replace(/^page\.evaluate: /, '').replace(/\s+at .*$/s, '').replace(/([0-9a-f]{8})-[0-9a-f-]{27}/g, '$1…').replace(/\s+/g, ' ').slice(0, 90);

const url = process.env.WEBMCP_URL;
if (!url) throw new Error("WEBMCP_URL is required");
const [width, height] = (process.env.WEBMCP_VIEWPORT ?? "1280x900").split("x").map(Number);

let page;
async function openPage() {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.WEBMCP_EXECUTABLE || undefined });
  const context = await browser.newContext({ viewport: { width, height } });
  await context.addInitScript(installModelContextBridge);
  if (process.env.WEBMCP_VIDEO_DIR) await context.addInitScript(installCallLog);
  page = await context.newPage();
  await page.goto(url);
  if (process.env.WEBMCP_VIDEO_DIR) captureFrames(join(process.env.WEBMCP_VIDEO_DIR, "frames"));
  const deadline = Date.now() + Number(process.env.WEBMCP_TIMEOUT_MS ?? 30_000);
  while (Date.now() < deadline) {
    if ((await listTools()).length) return;
    await page.waitForTimeout(250);
  }
  throw new Error(`no WebMCP tools registered at ${url}`);
}
const listTools = () => page.evaluate(() => globalThis.__wtModelContextBridge?.list() ?? []);

/** Recording mode: a JPEG of the page every 200ms until shutdown. */
async function captureFrames(dir) {
  mkdirSync(dir, { recursive: true });
  const start = Date.now();
  while (!closing) {
    const at = Date.now() - start;
    const jpeg = await page.screenshot({ type: "jpeg", quality: 75 }).catch(() => null);
    if (jpeg) writeFileSync(join(dir, `${String(at).padStart(7, "0")}.jpg`), jpeg);
    await new Promise(r => setTimeout(r, Math.max(0, 200 - (Date.now() - start - at))));
  }
}

/** Recording mode: log the call on the page, then reload so the UI shows its effect. */
async function showCall(name, args, response) {
  const text = response.content[0].text;
  const log = [name, shortArgs(args, text === "signed in" || /Authorization header missing/.test(text)), shortResult(text), !!response.isError];
  await page.evaluate(l => globalThis.__webmcpLog?.(...l), log).catch(() => {});
  await page.waitForTimeout(600);
  await page.goto(url).catch(() => {});
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline && !(await listTools().catch(() => [])).length) await page.waitForTimeout(200);
  await page.waitForTimeout(800);
}

const send = msg => process.stdout.write(JSON.stringify({ jsonrpc: "2.0", ...msg }) + "\n");
let ready;

async function handle(req) {
  switch (req.method) {
    case "initialize":
      ready ??= openPage();
      await ready;
      return {
        protocolVersion: req.params?.protocolVersion ?? "2025-06-18",
        capabilities: { tools: {} },
        serverInfo: { name: "webmcp-bridge", version: "0.1.0" },
      };
    case "tools/list":
      await ready;
      return { tools: await listTools() };
    case "tools/call": {
      await ready;
      const { name, arguments: args } = req.params;
      let response;
      try {
        const result = await page.evaluate(([n, a]) => globalThis.__wtModelContextBridge.execute(n, a), [name, args ?? {}]);
        response = { content: [{ type: "text", text: typeof result === "string" ? result : JSON.stringify(result ?? null) }] };
      } catch (error) {
        response = { content: [{ type: "text", text: String(error?.message ?? error) }], isError: true };
      }
      if (process.env.WEBMCP_VIDEO_DIR) await showCall(name, args, response);
      return response;
    }
    case "ping":
      return {};
    default:
      throw Object.assign(new Error(`method not found: ${req.method}`), { code: -32601 });
  }
}

// Requests run one at a time, in arrival order: parallel tool calls (say, sign_in and a list) must
// not race inside the one page.
let queue = Promise.resolve();
createInterface({ input: process.stdin }).on("line", line => {
  if (!line.trim()) return;
  const req = JSON.parse(line);
  if (req.id === undefined) return; // notifications (initialized, cancelled) need no reply
  queue = queue.then(async () => {
    try {
      send({ id: req.id, result: await handle(req) });
    } catch (error) {
      send({ id: req.id, error: { code: error.code ?? -32603, message: String(error.message ?? error) } });
    }
  });
});

// Close the browser on exit. Shutdown runs once, though SIGTERM and the end of stdin often arrive together.
let closing;
const shutdown = () =>
  (closing ??= (async () => {
    await page?.context().browser()?.close().catch(() => {});
    process.exit(0);
  })());
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, shutdown);
process.stdin.on("end", shutdown);
