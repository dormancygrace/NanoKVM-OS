import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const time = (page: Page) => page.getByLabel('Daily at', { exact: true });
const toggle = (page: Page) => page.getByRole('switch');
const events = (page: Page) => page.getByTestId('events');
const complete = (page: Page) => page.getByRole('button', { name: 'Complete write', exact: true });

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/index-schedule.html');
  await expect(time(page)).toHaveValue('03:00');
});

test('a switch click commits the edited time and toggle together', async ({ page }) => {
  await time(page).fill('04:15');
  await toggle(page).click();
  await expect(events(page)).toHaveText('PUT {"enabled":false,"time":"04:15"}');
  await expect(toggle(page)).toBeDisabled();
  await complete(page).click();
  await expect(toggle(page)).not.toBeChecked();
  await expect(time(page)).toHaveCount(0);
  await toggle(page).click();
  await expect(events(page)).toContainText('PUT {"enabled":true,"time":"04:15"}');
  await complete(page).click();
  await expect(time(page)).toHaveValue('04:15');
});

test('keyboard focus through the switch still saves the edited time', async ({ page }) => {
  await time(page).fill('05:30');
  await time(page).press('Shift+Tab');
  await expect(toggle(page)).toBeFocused();
  await expect(events(page)).toHaveText('');
  await toggle(page).press('Tab');
  await expect(events(page)).toHaveText('PUT {"enabled":true,"time":"05:30"}');
  await complete(page).click();
  await expect(time(page)).toHaveValue('05:30');
});

test('Enter saves once, and a refused write restores the saved time', async ({ page }) => {
  await time(page).fill('06:00');
  await time(page).press('Enter');
  await expect(events(page)).toHaveText('PUT {"enabled":true,"time":"06:00"}');
  await complete(page).click();
  await page.getByRole('button', { name: 'Refuse writes', exact: true }).click();
  await time(page).fill('07:00');
  await page.getByRole('button', { name: 'Outside schedule', exact: true }).click();
  await complete(page).click();
  await expect(page.getByText('Synthetic refusal', { exact: true })).toBeVisible();
  await expect(time(page)).toHaveValue('06:00');
  await expect(toggle(page)).toBeChecked();
});

test('an incomplete time does not prevent disabling the schedule', async ({ page }) => {
  await time(page).fill('');
  await toggle(page).click();
  await expect(events(page)).toHaveText('PUT {"enabled":false,"time":"03:00"}');
  await complete(page).click();
  await expect(toggle(page)).not.toBeChecked();
});
