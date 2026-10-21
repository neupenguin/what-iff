import type { Page } from '@playwright/test';

import { test, expect } from '../fixtures';

/**
 * Pixel identity between two ax levels (default 0 vs 4, i.e. tiers wcag vs patterns) on the
 * three states the axe scan uses. Fails on any byte difference.
 *   AX_PIXEL_LEVELS=0,3 npx playwright test --config e2e/ax-demo/playwright.config.ax-levels.ts --project=axe-ax0 pixels
 */
const [A, B] = (process.env['AX_PIXEL_LEVELS'] ?? '0,4').split(',').map(Number) as [number, number];

async function shots(page: Page, level: number, marker: string): Promise<Buffer[]> {
  await page.goto(`/memories?ax=${level}`);
  await expect(page.getByText(`${marker}-0`, { exact: true })).toBeVisible();
  await page.mouse.move(0, 0);
  const out = [await page.screenshot({ animations: 'disabled' })];
  await page
    .locator('app-memory-card')
    .filter({ hasText: `${marker}-0` })
    .getByText('⋮', { exact: true })
    .click();
  out.push(await page.screenshot({ animations: 'disabled' }));
  await page.getByText('Move', { exact: true }).click();
  await expect(page.getByText('Move to', { exact: true })).toBeVisible();
  await page.mouse.move(0, 0);
  out.push(await page.screenshot({ animations: 'disabled' }));
  return out;
}

test(`pixels identical at ax=${A} and ax=${B}`, async ({ seed, userWithPersonality }, testInfo) => {
  const { page } = userWithPersonality;
  const marker = `px-${Date.now().toString(36)}`;
  await seed.memories(6, { content: marker });
  const a = await shots(page, A, marker);
  const b = await shots(page, B, marker);
  const names = ['list', 'card-menu', 'move-menu'];
  const differing = names.filter((_, i) => !a[i]!.equals(b[i]!));
  for (const [i, name] of names.entries()) {
    await testInfo.attach(`${name}-ax${A}.png`, { body: a[i]!, contentType: 'image/png' });
    await testInfo.attach(`${name}-ax${B}.png`, { body: b[i]!, contentType: 'image/png' });
  }
  expect(differing, `states that differ between ax=${A} and ax=${B}`).toEqual([]);
});
