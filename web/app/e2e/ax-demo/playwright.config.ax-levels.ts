import { defineConfig, devices, type Project } from '@playwright/test';

import mockConfig from '../playwright.config.mock-llm';
import { chromiumHostResolverUse, localWebServer } from '../playwright.config.base';

/**
 * Eval-side signals for the agent-experience demo, one Playwright project per
 * level so a single run compares them side by side:
 *
 *   functional-ax<N>  the existing memory functional specs (role/label-based
 *                     POMs), unmodified, with the app pinned to level N
 *   axe-ax<N>         memories-axe.ax.spec.ts, an axe scan that reports and
 *                     never fails on violations
 *
 * The level is pinned through localStorage in the project's storageState,
 * which AxLevelService falls back to when the URL carries no `?ax`. That
 * keeps the functional specs byte-for-byte as they are on main.
 *
 *   npx playwright test --config e2e/ax-demo/playwright.config.ax-levels.ts
 *   npx playwright test --config e2e/ax-demo/playwright.config.ax-levels.ts --project='axe-*'
 *
 * Failures in the functional projects at levels 1-3 are the expected
 * measurement, not a broken build. Results land in e2e/ax-demo/results/ via
 * ax-level-reporter.ts. Inherits backend, timeouts and artifact retention from
 * the mock config; Chromium desktop only.
 */
const LEVELS = [0, 1, 2, 3] as const;
const baseURL = process.env['E2E_BASE_URL'] ?? 'http://localhost:4200';

function levelState(level: number) {
  return {
    cookies: [],
    origins: [{ origin: new URL(baseURL).origin, localStorage: [{ name: 'whatiff.ax-level', value: String(level) }] }],
  };
}

const projects: Project[] = LEVELS.flatMap(level => [
  {
    name: `functional-ax${level}`,
    testDir: '../tests/functional/memory',
    metadata: { axLevel: level, axKind: 'functional' },
    use: { ...devices['Desktop Chrome'], ...chromiumHostResolverUse, storageState: levelState(level) },
  },
  {
    name: `axe-ax${level}`,
    testDir: '.',
    testMatch: /memories-axe\.ax\.spec\.ts$/,
    metadata: { axLevel: level, axKind: 'axe' },
    use: { ...devices['Desktop Chrome'], ...chromiumHostResolverUse, storageState: levelState(level) },
  },
]);

export default defineConfig({
  ...mockConfig,
  testDir: '.',
  testIgnore: undefined,
  grepInvert: /@visual/,
  // Never retry here: a retry would turn a level-induced failure into a pass.
  retries: 0,
  reporter: [['list'], ['./ax-level-reporter.ts']],
  use: { ...mockConfig.use, baseURL },
  projects,
  // Same dev server as every local config; cwd is the app root, two levels up from this file.
  webServer: process.env['E2E_REUSE_HOST_WEBSERVER'] === '1' ? undefined : { ...localWebServer, cwd: '../..', timeout: 120 * 1000 },
});
