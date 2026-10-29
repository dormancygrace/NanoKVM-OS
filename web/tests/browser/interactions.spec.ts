import type { APIRequestContext, CDPSession, Locator, Page } from '@playwright/test';

import { expect, test } from './fixtures';

const url = '/tests/browser/index.html';
async function login(page: Page) {
  await page.goto(url);
  await page.getByPlaceholder('Username').fill('admin');
  await page.getByPlaceholder('Password', { exact: true }).fill('browser-test-password');
  await page.getByRole('button', { name: /Sign in/ }).click();
  await expect(page.getByTestId('connection')).toHaveText('connected / control');
}
async function reports(request: APIRequestContext): Promise<number[][]> {
  return (await (await request.get('/__test/state')).json()).reports;
}
async function openKeyboard(page: Page) {
  await page.getByRole('textbox', { name: 'Video focus target' }).focus();
  await page.getByRole('button', { name: 'Open keyboard', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
}
const key = (page: Page, code = 'KeyA') => page.locator(`[data-skbtn="${code}"]`).first();

// Native pointer delivery is required: DOM-dispatched events cannot test capture
// or browser gesture arbitration.
async function touchTap(cdp: CDPSession, target: Locator) {
  await expect(target).toBeVisible();
  const box = await target.boundingBox();
  if (!box) throw new Error('touch target not visible');
  await cdp.send('Input.dispatchTouchEvent', {
    type: 'touchStart',
    touchPoints: [{ x: box.x + box.width / 2, y: box.y + box.height / 2 }]
  });
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
}

test.beforeEach(async ({ request }) => {
  await request.post('/__test/reset');
});

test('login errors, cookie auth and native document metadata', async ({ page, request }) => {
  await page.goto(url);
  await page.getByPlaceholder('Username').fill('admin');
  await page.getByPlaceholder('Password', { exact: true }).fill('wrong');
  await page.getByRole('button', { name: /Sign in/ }).click();
  await expect(page.getByText('Invalid username or password', { exact: true })).toBeVisible();
  await page.getByPlaceholder('Password', { exact: true }).fill('browser-test-password');
  await page.getByRole('button', { name: /Sign in/ }).click();
  await expect(page.getByTestId('connection')).toHaveText('connected / control');
  expect((await (await request.get('/__test/state')).json()).loginEncoding).toBe('plain');
  expect((await page.context().cookies()).find((c) => c.name === 'fixture-session')?.httpOnly).toBe(
    true
  );
  await expect(page.locator('head title')).toHaveCount(1);
  await expect(page.locator('head link[rel="icon"]')).toHaveCount(1);
  await page.getByRole('button', { name: 'Change title' }).click();
  await expect(page).toHaveTitle(/Updated fixture/);
  await expect(page.locator('head title')).toHaveCount(1);
});

test('keyboard keeps background focus and restores it on close and Escape', async ({ page }) => {
  await login(page);
  await openKeyboard(page);
  const target = page.getByRole('textbox', { name: 'Video focus target' });
  await expect(target).toBeFocused();
  await target.fill('background remains interactive');
  await page.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(target).toBeFocused();
  await openKeyboard(page);
  await page.getByRole('button', { name: 'Close', exact: true }).focus();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(target).toBeFocused();
  await openKeyboard(page);
  await page.locator('[data-skbtn="{shiftleft}"]').first().click();
  await page.getByRole('button', { name: 'Close', exact: true }).click();
  await openKeyboard(page);
  await expect(page.locator('.hg-highlight')).toHaveCount(0);
  await page.getByRole('button', { name: 'Unmount keyboard' }).click();
  // Closing from outside must not take focus away from that outside control.
  await expect(page.getByRole('button', { name: 'Unmount keyboard' })).toBeFocused();
});

test('password help opens lazily and fits a narrow viewport', async ({ page }) => {
  await page.setViewportSize({ width: 393, height: 852 });
  await page.goto(url);
  await page.getByRole('button', { name: 'Forgot password' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toBeVisible();
  expect(await dialog.evaluate((e) => e.scrollWidth <= e.clientWidth)).toBe(true);
  await dialog.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(dialog).not.toBeVisible();
});

test('held keys release outside the key, on blur and on unmount', async ({ page, request }) => {
  await login(page);
  await openKeyboard(page);
  const box = await key(page).boundingBox();
  if (!box) throw new Error('key not visible');
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await expect.poll(() => reports(request).then((r) => r.some((x) => x[3] === 4))).toBe(true);
  await page.mouse.move(20, 20);
  await page.mouse.up();
  await expect
    .poll(() => reports(request).then((r) => r[r.length - 1]?.slice(1)))
    .toEqual([0, 0, 0, 0, 0, 0, 0, 0]);
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.evaluate(() => window.dispatchEvent(new Event('blur')));
  await expect
    .poll(() => reports(request).then((r) => r[r.length - 1]?.slice(1)))
    .toEqual([0, 0, 0, 0, 0, 0, 0, 0]);
  await page.mouse.up();
  await page.mouse.down();
  // Keyboard activation of an external control unmounts while the pointer is held.
  await page.getByRole('button', { name: 'Unmount keyboard' }).focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect
    .poll(() => reports(request).then((r) => r[r.length - 1]?.slice(1)))
    .toEqual([0, 0, 0, 0, 0, 0, 0, 0]);
  await page.mouse.up();
});

test('reconnect does not replay held input and expired auth stops reconnecting', async ({
  page,
  request
}) => {
  await login(page);
  await openKeyboard(page);
  const box = await key(page).boundingBox();
  if (!box) throw new Error('key not visible');
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await expect.poll(() => reports(request).then((r) => r.some((x) => x[3] === 4))).toBe(true);
  await request.post('/__test/drop');
  await expect(page.getByTestId('connection')).toHaveText('reconnecting / no control');
  await page.mouse.up();
  await request.post('/__test/reports/reset');
  await expect(page.getByTestId('connection')).toHaveText('connected / control');
  expect(await reports(request)).toEqual([]);
  await key(page).click();
  await expect.poll(() => reports(request).then((r) => r.some((x) => x[3] === 4))).toBe(true);
  await request.post('/__test/expire');
  await expect(page.getByPlaceholder('Username')).toBeVisible();
  const count = (await (await request.get('/__test/state')).json()).attempts;
  // Longer than the production reconnect interval, to catch unwanted retries.
  await page.waitForTimeout(3500);
  expect((await (await request.get('/__test/state')).json()).attempts).toBe(count);
});

test.describe('touch', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 393, height: 852 } });
  test('a fresh touch swipe can close and reopen the keyboard', async ({ page }) => {
    await login(page);
    const cdp = await page.context().newCDPSession(page);
    const opener = page.getByRole('button', { name: 'Open keyboard', exact: true });
    await touchTap(cdp, opener);
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    const box = await dialog.locator('[aria-hidden="true"]').first().boundingBox();
    if (!box) throw new Error('handle not visible');
    const x = box.x + box.width / 2,
      y = box.y + box.height / 2;
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchMove',
      touchPoints: [{ x, y: y + 90 }]
    });
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    await expect(dialog).toHaveCount(0);
    await touchTap(cdp, opener);
    await expect(dialog).toHaveCSS('transform', 'matrix(1, 0, 0, 1, 0, 0)');
  });

  test('touch keys release; only the handle drags the sheet; cancelled drags reset', async ({
    page,
    request
  }) => {
    await login(page);
    // Include native event order in failure traces, without adding CI log noise.
    await page.evaluate(() => {
      for (const type of [
        'pointerdown',
        'pointerup',
        'pointercancel',
        'lostpointercapture',
        'touchstart',
        'touchend',
        'touchcancel',
        'click'
      ]) {
        document.addEventListener(type, (event) => {
          const e = event as PointerEvent;
          const target = e.target as HTMLElement;
          console.debug(
            'touch input',
            JSON.stringify({
              type,
              target: target.tagName,
              text: target.textContent?.slice(0, 30),
              prevented: e.defaultPrevented,
              pointer: e.pointerId,
              x: e.clientX,
              y: e.clientY,
              time: performance.now()
            })
          );
        });
      }
    });
    const cdp = await page.context().newCDPSession(page);
    await touchTap(cdp, page.getByRole('button', { name: 'Open keyboard', exact: true }));
    await expect(page.getByRole('dialog')).toBeVisible();
    await touchTap(cdp, key(page));
    await expect.poll(() => reports(request).then((r) => r.some((x) => x[3] === 4))).toBe(true);
    await expect
      .poll(() => reports(request).then((r) => r[r.length - 1]?.slice(1)))
      .toEqual([0, 0, 0, 0, 0, 0, 0, 0]);
    const dialog = page.getByRole('dialog');
    const keyBox = await key(page).boundingBox();
    if (!keyBox) throw new Error('key not visible');
    const kx = keyBox.x + keyBox.width / 2,
      ky = keyBox.y + keyBox.height / 2;
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchStart',
      touchPoints: [{ x: kx, y: ky }]
    });
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchMove',
      touchPoints: [{ x: kx, y: ky + 20 }]
    });
    // Dragging a pressed key must not turn into native page scrolling and
    // cancel its held HID report before the finger is released.
    await expect.poll(() => reports(request).then((r) => r[r.length - 1]?.[3])).toBe(4);
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    await expect
      .poll(() => reports(request).then((r) => r[r.length - 1]?.slice(1)))
      .toEqual([0, 0, 0, 0, 0, 0, 0, 0]);
    await expect(dialog).toHaveCSS('transform', 'matrix(1, 0, 0, 1, 0, 0)');
    const handle = dialog.locator('[aria-hidden="true"]').first();
    const box = await handle.boundingBox();
    if (!box) throw new Error('handle not visible');
    const x = box.x + box.width / 2,
      y = box.y + box.height / 2;
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchMove',
      touchPoints: [{ x, y: y + 35 }]
    });
    await expect(dialog).toHaveCSS('transform', 'matrix(1, 0, 0, 1, 0, 35)');
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchCancel', touchPoints: [] });
    await expect(dialog).toHaveCSS('transform', 'matrix(1, 0, 0, 1, 0, 0)');
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchMove',
      touchPoints: [{ x, y: y + 90 }]
    });
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    await expect(dialog).toHaveCount(0);
    await touchTap(cdp, page.getByRole('button', { name: 'Open keyboard', exact: true }));
    await expect(page.getByRole('dialog')).toHaveCSS('transform', 'matrix(1, 0, 0, 1, 0, 0)');
  });
});
