import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const events = (page: Page) => page.getByTestId('events');
const save = (page: Page) => page.getByRole('button', { name: 'Save', exact: true });
const complete = (page: Page) => page.getByRole('button', { name: 'Complete save' });
// Count channel folds, excluding the standard preview and template editors.
const folds = (page: Page) => page.locator('button[aria-expanded][aria-controls^="notify-k"]');
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
  await page.getByLabel('Message language').click();
  await page.getByTitle('Русский').click();
  await complete(page).click();
  await expect(events(page)).toContainText('PUT committed');
  await expect(save(page)).toBeEnabled();
  await save(page).click();
  await expect(events(page)).toContainText('PUT held language=ru');
});

const addShortTemplate = async (page: Page) => {
  await page.getByRole('button', { name: 'Add template' }).click();
  await page.getByRole('textbox', { name: 'Template name' }).fill('Short');
  await page.getByRole('textbox', { name: 'Host powered off: Title' }).fill('OFF');
  await page.getByLabel('Message template').click();
  await page.getByTitle('Short').click();
};

test('a template is previewed, picked by a channel and saved', async ({ page }) => {
  await addShortTemplate(page);
  await page.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect(events(page)).toContainText('PREVIEW language=en template=Short');
  await expect(page.getByText('OFF', { exact: true })).toBeVisible();
  await save(page).click();
  await expect(events(page)).toContainText(
    'TEMPLATES Short={"power.off":{"title":"OFF","body":""}} USED Short'
  );
  await complete(page).click();
  await expect(events(page)).toContainText('PUT committed');
  await expect(save(page)).toBeDisabled();
  await expect(page.getByLabel('Message template')).toBeVisible();
});

test('a deleted template leaves its channels on the standard messages', async ({ page }) => {
  await addShortTemplate(page);
  await page.getByRole('button', { name: 'Delete template' }).click();
  await page.getByRole('button', { name: 'OK' }).click();
  await save(page).click();
  await expect(events(page)).toContainText('TEMPLATES  USED standard');
});

test('the standard messages are previewed in the chosen language', async ({ page }) => {
  await page.getByLabel('Message language').click();
  await page.getByTitle('Русский').click();
  await page.getByRole('button', { name: 'Preview the standard messages' }).click();
  await expect(events(page)).toContainText('PREVIEW language=ru template=standard');
});
