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
 * WEBMCP_VIDEO_DIR (record the page), WEBMCP_TIMEOUT_MS=30000 (wait for tools to register).
 */
import { chromium } from "@playwright/test";
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

const url = process.env.WEBMCP_URL;
if (!url) throw new Error("WEBMCP_URL is required");
const [width, height] = (process.env.WEBMCP_VIEWPORT ?? "1280x900").split("x").map(Number);

let page;
async function openPage() {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.WEBMCP_EXECUTABLE || undefined });
  const context = await browser.newContext({
    viewport: { width, height },
    ...(process.env.WEBMCP_VIDEO_DIR ? { recordVideo: { dir: process.env.WEBMCP_VIDEO_DIR, size: { width, height } } } : {}),
  });
  await context.addInitScript(installModelContextBridge);
  page = await context.newPage();
  await page.goto(url);
  const deadline = Date.now() + Number(process.env.WEBMCP_TIMEOUT_MS ?? 30_000);
  while (Date.now() < deadline) {
    if ((await listTools()).length) return;
    await page.waitForTimeout(250);
  }
  throw new Error(`no WebMCP tools registered at ${url}`);
}
const listTools = () => page.evaluate(() => globalThis.__wtModelContextBridge?.list() ?? []);

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
      try {
        const result = await page.evaluate(([n, a]) => globalThis.__wtModelContextBridge.execute(n, a), [name, args ?? {}]);
        return { content: [{ type: "text", text: typeof result === "string" ? result : JSON.stringify(result ?? null) }] };
      } catch (error) {
        return { content: [{ type: "text", text: String(error?.message ?? error) }], isError: true };
      }
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

// Close the browser on exit so a recorded video is finalised.
for (const signal of ["SIGTERM", "SIGINT"])
  process.on(signal, async () => {
    await page
      ?.context()
      .close()
      .catch(() => {});
    process.exit(0);
  });
process.stdin.on("end", async () => {
  await page
    ?.context()
    .close()
    .catch(() => {});
  process.exit(0);
});
