import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const events = (page: Page) => page.getByTestId('events');
const save = (page: Page) => page.getByRole('button', { name: 'Save', exact: true });
const complete = (page: Page) => page.getByRole('button', { name: 'Complete save' });
const folds = (page: Page) => page.locator('button[aria-expanded]');
const names = (page: Page) => page.getByRole('textbox', { name: 'Name', exact: true });

const addTelegram = async (page: Page) => {
  await page.getByRole('button', { name: 'Add channel' }).click();
  await page.getByRole('menuitem', { name: 'Telegram' }).click();
};

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/notifications.html');
  await expect(folds(page)).toHaveCount(1);
  await folds(page).first().click();
  await expect(names(page)).toHaveValue('Before');
});

test('an edit made while Save is pending stays and is saved next', async ({ page }) => {
  await names(page).fill('Before save');
  await save(page).click();
  await expect(events(page)).toContainText('PUT held language=en c1:Before save');
  await names(page).fill('Edited while saving');
  await complete(page).click();
  await expect(events(page)).toContainText('PUT committed');
  await expect(names(page)).toHaveValue('Edited while saving');
  await expect(save(page)).toBeEnabled();
  await save(page).click();
  await expect(events(page)).toContainText('PUT held language=en c1:Edited while saving');
});

test('a save with no edit meanwhile shows the stored list', async ({ page }) => {
  await names(page).fill('Saved');
  await save(page).click();
  await complete(page).click();
  await expect(events(page)).toContainText('PUT committed');
  await expect(names(page)).toHaveValue('Saved');
  await expect(save(page)).toBeDisabled();
  // Folding is not an edit.
  await folds(page).first().click();
  await expect(save(page)).toBeDisabled();
});

test('channels added or removed while Save is pending stay that way', async ({ page }) => {
  await addTelegram(page);
  await names(page).nth(1).fill('Sent new');
  await save(page).click();
  await expect(events(page)).toContainText('PUT held language=en c1:Before,new:Sent new');
  // Meanwhile: another new channel, and the first one deleted.
  await addTelegram(page);
  await names(page).nth(2).fill('Added while saving');
  await page.getByRole('button', { name: 'Delete channel' }).first().click();
  await page.getByRole('button', { name: 'OK' }).click();
  await complete(page).click();
  await expect(events(page)).toContainText('PUT committed');
  await expect(folds(page)).toHaveCount(2);
  await expect(names(page).nth(0)).toHaveValue('Sent new');
  await expect(names(page).nth(1)).toHaveValue('Added while saving');
  await expect(save(page)).toBeEnabled();
  // The channel sent first keeps the id the device gave it.
  await save(page).click();
  await expect(events(page)).toContainText(
    'PUT held language=en c2:Sent new,new:Added while saving'
  );
});

test('a language chosen while Save is pending stays', async ({ page }) => {
  await names(page).fill('Before save');
  await save(page).click();
  await expect(events(page)).toContainText('PUT held language=en');
  await page.getByLabel('Language of the messages').click();
  await page.getByTitle('Русский').click();
  await complete(page).click();
  await expect(events(page)).toContainText('PUT committed');
  await expect(save(page)).toBeEnabled();
  await save(page).click();
  await expect(events(page)).toContainText('PUT held language=ru');
});
