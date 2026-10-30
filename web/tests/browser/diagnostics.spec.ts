import { expect, test } from './fixtures';

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/diagnostics.html');
  await expect(page.getByText('Everything is OK', { exact: true })).toBeVisible();
});

test('diagnostics treats optional services, no signal and disabled USB as normal', async ({
  page
}) => {
  await expect(page.getByText('Optional, not running', { exact: false })).toBeVisible();
  await expect(page.getByText('USB is switched off in USB Gadget settings.')).toBeVisible();
  await expect(page.getByText('fixture-package', { exact: true })).not.toBeVisible();
  await page.getByText('Packages (1)', { exact: true }).click();
  await expect(page.getByText('fixture-package', { exact: true })).toBeVisible();
});

test('diagnostics includes normalized APK errors in the summary', async ({ page }) => {
  await page.getByRole('button', { name: 'APK failure', exact: true }).click();
  await expect(page.getByText('Everything is OK', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('listitem')).toHaveCount(1);
  await expect(page.getByRole('listitem')).toContainText('APK: error');
  await expect(page.getByRole('listitem')).toContainText('The latest operation failed');
});

test('diagnostics includes pending EDID power cycles in the summary', async ({ page }) => {
  await page.getByRole('button', { name: 'EDID pending', exact: true }).click();
  await expect(page.getByText('Everything is OK', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('listitem')).toHaveText(
    'EDID programming: physical power cycle pending'
  );
});

test('diagnostics counts and names every simultaneous problem', async ({ page }) => {
  await page.getByRole('button', { name: 'Multiple problems', exact: true }).click();
  await expect(page.getByText('Everything is OK', { exact: true })).toHaveCount(0);
  const items = page.getByRole('listitem');
  await expect(items).toHaveCount(8);
  for (const text of [
    'Kernel modules: error',
    'Boot components: reboot pending',
    'Controller binding: error',
    'nftables: error',
    'NanoKVM: stopped',
    'optional-vpn: error',
    'APK: error',
    'EDID programming: physical power cycle pending'
  ])
    await expect(items.filter({ hasText: text })).toHaveCount(1);
});

for (const width of [393, 1280]) {
  test(`wrapped log search keeps next/previous matches visible at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.getByRole('tab', { name: 'Logs', exact: true }).click();
    const search = page.getByRole('textbox', { name: 'Search this log', exact: true });
    const mark = page.locator('pre mark');
    const visibleMatch = async (text: string) => {
      await expect(mark).toHaveText(text);
      await expect
        .poll(() =>
          mark.evaluate((node) => {
            const viewer = node.closest('pre')!;
            const outer = viewer.getBoundingClientRect();
            const target = node.getBoundingClientRect();
            return target.top >= outer.top && target.bottom <= outer.bottom;
          })
        )
        .toBe(true);
    };
    await search.fill('needle');
    await visibleMatch('needle first');
    await expect(page.getByText('1 / 2', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Next matching line', exact: true }).click();
    await visibleMatch('needle second');
    await expect(page.getByText('2 / 2', { exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Previous matching line', exact: true }).click();
    await visibleMatch('needle first');
    await page.getByRole('switch', { name: 'Only matching lines', exact: true }).click();
    await visibleMatch('needle first');
    await expect(page.locator('pre')).toHaveText('needle first\nneedle second');
    await search.fill('no-such-line');
    await expect(page.getByText('0 / 0', { exact: true })).toBeVisible();
    await expect(
      page.getByRole('button', { name: 'Next matching line', exact: true })
    ).toBeDisabled();
    await search.fill('');
    await expect(mark).toHaveCount(0);
    await expect(page.getByText('0 / 0', { exact: true })).toHaveCount(0);
  });
}
