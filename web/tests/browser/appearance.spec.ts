import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const input = (page: Page) =>
  page.getByRole('textbox', { name: 'Button color in hexadecimal format', exact: true });
const reset = (page: Page) => page.getByRole('button', { name: 'Restore default', exact: true });
const events = (page: Page) => page.getByTestId('events');
const complete = (page: Page) => page.getByRole('button', { name: 'Complete write', exact: true });

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/appearance.html');
  await expect(input(page)).toHaveValue('#112233');
  await page.getByRole('button', { name: 'Hold writes', exact: true }).click();
});

test('color reset wins over input blur autosave', async ({ page }) => {
  await input(page).fill('#AABBCC');
  await reset(page).click();
  await expect(events(page)).toContainText('DELETE /api/branding/button-color');
  await expect(events(page)).not.toContainText('POST /api/branding/button-color');
  await complete(page).click();
  await expect(input(page)).toHaveValue('#45E9A0');
  await expect(reset(page)).not.toBeVisible();
});

test('color reset is reachable and activatable from the keyboard', async ({ page }) => {
  await input(page).fill('#AABBCC');
  await input(page).press('Tab');
  await expect(reset(page)).toBeFocused();
  await expect(events(page)).not.toContainText('POST /api/branding/button-color');
  await reset(page).press('Enter');
  await expect(events(page)).toContainText('DELETE /api/branding/button-color');
  await complete(page).click();
  await expect(input(page)).toHaveValue('#45E9A0');
});

test('tabbing past reset still saves the unsubmitted color', async ({ page }) => {
  await input(page).fill('#AABBCC');
  await input(page).press('Tab');
  await reset(page).press('Tab');
  await expect(events(page)).toContainText('POST /api/branding/button-color {"color":"#AABBCC"}');
  await expect(events(page)).not.toContainText('DELETE /api/branding/button-color');
  await complete(page).click();
  await expect(input(page)).toHaveValue('#AABBCC');
});

test('a rejected color save restores the confirmed color', async ({ page }) => {
  await page.getByRole('button', { name: 'Refuse writes', exact: true }).click();
  await input(page).fill('#AABBCC');
  await input(page).press('Enter');
  await expect(events(page)).toContainText('POST /api/branding/button-color');
  await complete(page).click();
  await expect(page.getByText('Could not save the button color.', { exact: true })).toBeVisible();
  await expect(input(page)).toHaveValue('#112233');
});
