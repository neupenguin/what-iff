import AxeBuilder from '@axe-core/playwright';
import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';

/**
 * axe-core scan of /memories at one agent-experience level (the project's
 * `metadata.axLevel`, see playwright.config.ax-levels.ts). REPORTS, never
 * fails on violations: the counts are attached to the result and
 * ax-level-reporter.ts writes them to results/axe-by-level.json.
 *
 * Three states: the populated list, the card Move menu, and the "Move to a
 * thread" dialog (ui-modal). The menu is reached by visible text rather than
 * through the MemoriesPage POM, because the POM's role-based locators are
 * exactly what the higher levels break — that breakage is measured separately
 * by the functional projects, not here.
 */

const WCAG_TAGS = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'best-practice'];

interface StateScan {
  state: string;
  violationRules: number;
  violationNodes: number;
  byRule: Record<string, { impact: string | null; nodes: number }>;
}

async function scan(page: Page, state: string): Promise<StateScan> {
  const result = await new AxeBuilder({ page }).include('app-memories-page').withTags(WCAG_TAGS).analyze();
  const byRule: StateScan['byRule'] = {};
  for (const violation of result.violations) {
    byRule[violation.id] = { impact: violation.impact ?? null, nodes: violation.nodes.length };
  }
  return {
    state,
    violationRules: result.violations.length,
    violationNodes: result.violations.reduce((sum, violation) => sum + violation.nodes.length, 0),
    byRule,
  };
}

test('axe violations on /memories at this ax level', async ({ seed, userWithPersonality }, testInfo) => {
  // A signed-in account that owns a personality (the setup guard needs one); its page is the test's page.
  const { page } = userWithPersonality;
  const level = Number(testInfo.project.metadata['axLevel'] ?? 0);
  const marker = `axe-scan-${Date.now().toString(36)}`;
  await seed.memories(6, { content: marker });

  await page.goto(`/memories?ax=${level}`);
  const firstCard = page.getByText(`${marker}-0`, { exact: true });
  await expect(firstCard).toBeVisible();

  const scans: StateScan[] = [await scan(page, 'populated')];

  // The card's ⋮ menu, then Move: the move menu overlay.
  await page.locator('app-memory-card').filter({ hasText: `${marker}-0` }).getByText('⋮', { exact: true }).click();
  await page.getByText('Move', { exact: true }).click();
  await expect(page.getByText('Move to', { exact: true })).toBeVisible();
  scans.push(await scan(page, 'move-menu'));

  // "A thread…" opens the ui-modal scope dialog.
  await page.getByText('A thread…', { exact: true }).click();
  await expect(page.getByText('Move this memory to a thread', { exact: true })).toBeVisible();
  scans.push(await scan(page, 'move-thread-dialog'));

  await testInfo.attach('axe-summary', {
    contentType: 'application/json',
    body: JSON.stringify({ level, scans }),
  });
});
