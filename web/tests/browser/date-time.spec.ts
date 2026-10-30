import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const events = (page: Page) => page.getByTestId('events');
const complete = (page: Page) => page.getByRole('button', { name: 'Complete write', exact: true });
const save = (page: Page) => page.getByRole('button', { name: 'Save', exact: true });
const clock = (page: Page) =>
  page.getByRole('button', { name: "Set to this browser's time", exact: true });

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/date-time.html');
  await expect(page.getByTestId('preferences')).toContainText('0.pool.ntp.org');
});

async function hour12(page: Page) {
  await page.getByRole('combobox', { name: 'Time format', exact: true }).click();
  await page.getByText('12 hours', { exact: true }).click();
}

test('date/time selects keep draft NTP servers out of their requests', async ({ page }) => {
  await page.getByRole('combobox', { name: 'NTP servers', exact: true }).fill('time.example.org');
  await page.getByRole('combobox', { name: 'NTP servers', exact: true }).press('Enter');
  // Close the tag suggestions before opening the select below them.
  await page.getByRole('combobox', { name: 'NTP servers', exact: true }).press('Escape');
  await hour12(page);
  await expect(events(page)).toContainText('"servers":["0.pool.ntp.org"]');
  await expect(events(page)).not.toContainText('time.example.org');
  await complete(page).click();
  await expect(save(page)).toBeEnabled();
  await save(page).click();
  await expect(events(page)).toContainText('"servers":["0.pool.ntp.org","time.example.org"]');
  await complete(page).click();
  await expect(save(page)).toBeDisabled();
  await expect(page.getByTestId('preferences')).toContainText('time.example.org');
});

test('date/time ignores a read started before an immediate format change', async ({ page }) => {
  await page.getByRole('button', { name: 'Hold poll', exact: true }).click();
  await expect(events(page)).toContainText('GET held');
  await hour12(page);
  await complete(page).click();
  await expect(page.getByTestId('preferences')).toContainText('"format":"12"');
  await page.getByRole('button', { name: 'Release read', exact: true }).click();
  await expect(events(page)).toContainText('RELEASE read\nGET delivered format=24');
  await expect(page.getByTestId('preferences')).toContainText('"format":"12"');
  await page.getByRole('combobox', { name: 'Time zone', exact: true }).click();
  await page.getByText('Europe/Berlin', { exact: true }).click();
  await expect(events(page)).toContainText('"timezone":"Europe/Berlin","format":"12"');
});

test('browser clock handles success, partial failure and NTP synchronization', async ({ page }) => {
  await clock(page).click();
  await expect(events(page)).toContainText('CLOCK requested');
  await complete(page).click();
  await expect(events(page)).toContainText('CLOCK received valid browser time=true');
  await expect(clock(page)).toBeEnabled();
  await page.getByRole('button', { name: 'Refuse writes', exact: true }).click();
  await clock(page).click();
  await expect(page.getByText(/System clock set, but could not save it to the RTC/)).toBeVisible();
  await page.getByRole('button', { name: 'NTP synchronized', exact: true }).click();
  await expect(clock(page)).not.toBeVisible();
});
