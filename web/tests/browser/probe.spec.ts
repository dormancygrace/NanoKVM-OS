// PROBE ONLY (draft PR, never merged): which part of the lost first tap after
// a fast handle flick depends on what. Prints native event order to the log.
import type { CDPSession, Locator, Page } from '@playwright/test';

import { expect, test } from './fixtures';

const url = '/tests/browser/index.html';
async function login(page: Page) {
  await page.goto(url);
  await page.getByPlaceholder('Username').fill('admin');
  await page.getByPlaceholder('Password', { exact: true }).fill('browser-test-password');
  await page.getByRole('button', { name: /Sign in/ }).click();
  await expect(page.getByTestId('connection')).toHaveText('connected / control');
}

async function center(target: Locator) {
  const box = await target.boundingBox();
  if (!box) throw new Error('not visible');
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
}

async function tap(cdp: CDPSession, page: Page, x: number, y: number) {
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  await page.waitForTimeout(50);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

async function flick(page: Page, cdp: CDPSession, x: number, y: number, fast: boolean) {
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  const steps = fast ? 3 : 6;
  for (let step = 1; step <= steps; step++) {
    await page.waitForTimeout(fast ? 16 : 30);
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchMove',
      touchPoints: [{ x, y: y + step * (fast ? 30 : 15) }]
    });
  }
  if (!fast) await page.waitForTimeout(50);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

async function installLogger(page: Page) {
  await page.evaluate(() => {
    const log: string[] = [];
    (window as unknown as { probeLog: string[] }).probeLog = log;
    for (const type of [
      'pointerdown',
      'pointerup',
      'pointercancel',
      'gotpointercapture',
      'lostpointercapture',
      'touchstart',
      'touchend',
      'touchcancel',
      'mousedown',
      'mouseup',
      'click',
      'contextmenu'
    ]) {
      document.addEventListener(
        type,
        (event) => {
          const target = event.target as HTMLElement;
          const label =
            target.getAttribute?.('aria-label') || target.textContent?.slice(0, 16) || '';
          log.push(
            `${Math.round(performance.now())} ${type} ${target.tagName}[${label}]${event.defaultPrevented ? ' prevented' : ''}`
          );
        },
        true
      );
    }
  });
}

const cases = [
  { name: 'A fast+production', fast: true, opener: 'Keyboard', pause: 0 },
  { name: 'B fast+workbench', fast: true, opener: 'Open keyboard', pause: 0 },
  { name: 'C slow+production', fast: false, opener: 'Keyboard', pause: 0 },
  { name: 'D fast+production+500ms', fast: true, opener: 'Keyboard', pause: 500 }
];

test.describe('touch probe', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 393, height: 852 } });
  for (const c of cases) {
    test(c.name, async ({ page }) => {
      await login(page);
      await installLogger(page);
      const cdp = await page.context().newCDPSession(page);
      const opener = page.getByRole('button', { name: c.opener, exact: true });
      const at = await center(opener);
      await tap(cdp, page, at.x, at.y);
      const dialog = page.getByRole('dialog');
      await expect(dialog).toBeVisible();
      const handle = await center(dialog.locator('[aria-hidden="true"]').first());
      await page.evaluate(() =>
        (window as unknown as { probeLog: string[] }).probeLog.push('--- flick')
      );
      await flick(page, cdp, handle.x, handle.y, c.fast);
      await expect(dialog).toHaveCount(0);
      if (c.pause) await page.waitForTimeout(c.pause);
      const hit = await page.evaluate(({ x, y }) => {
        const el = document.elementFromPoint(x, y) as HTMLElement | null;
        return `${el?.tagName}[${el?.getAttribute('aria-label') || el?.textContent?.slice(0, 16)}] active=${document.activeElement?.tagName}`;
      }, at);
      await page.evaluate(() =>
        (window as unknown as { probeLog: string[] }).probeLog.push('--- tap 1')
      );
      await tap(cdp, page, at.x, at.y);
      await page.waitForTimeout(1000);
      const first = await dialog.count();
      let second = -1;
      if (!first) {
        await page.evaluate(() =>
          (window as unknown as { probeLog: string[] }).probeLog.push('--- tap 2')
        );
        await tap(cdp, page, at.x, at.y);
        await page.waitForTimeout(1000);
        second = await dialog.count();
      }
      const log = await page.evaluate(() => (window as unknown as { probeLog: string[] }).probeLog);
      console.log(
        `PROBE ${c.name}: hit=${hit} firstTapOpens=${first} secondTapOpens=${second}\n  ${log.join('\n  ')}`
      );
    });
  }
});
