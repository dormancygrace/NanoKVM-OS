import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const events = (page: Page) => page.getByTestId('events');
const name = (page: Page) => page.getByRole('textbox', { name: 'Name', exact: true });
const save = (page: Page) => page.getByRole('button', { name: 'Save', exact: true });

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/addon.html');
  await expect(name(page)).toHaveValue('initial');
});

test('add-on save preserves edits made while the POST is pending', async ({ page }) => {
  await name(page).fill('draft A');
  await save(page).click();
  await expect(events(page)).toContainText('POST held name=draft A');
  await name(page).fill('draft B');
  await page.getByRole('button', { name: 'Complete save' }).click();
  await expect(events(page)).toContainText('GET settings delivered name=draft A');
  await expect(name(page)).toHaveValue('draft B');
});

test('add-on form ignores responses from a replaced manifest', async ({ page }) => {
  await page.getByRole('button', { name: 'Hold old source' }).click();
  await expect(events(page)).toContainText('GET old held');
  await page.getByRole('button', { name: 'Switch source' }).click();
  await expect(name(page)).toHaveValue('CURRENT NEW');
  await page.getByRole('button', { name: 'Release old read' }).click();
  await expect(events(page)).toContainText('GET old delivered');
  await expect(name(page)).toHaveValue('CURRENT NEW');
  await save(page).click();
  await expect(events(page)).toContainText('POST held name=CURRENT NEW');
});

test('add-on save invalidates a poll started before the mutation', async ({ page }) => {
  await page.getByRole('button', { name: 'Hold next poll' }).click();
  await expect(events(page)).toContainText('GET settings held name=initial');
  await name(page).fill('saved change');
  await save(page).click();
  await expect(events(page)).toContainText('POST held name=saved change');
  await page.getByRole('button', { name: 'Complete save' }).click();
  await expect(events(page)).toContainText('GET settings delivered name=saved change');
  await page.getByRole('button', { name: 'Release old read' }).click();
  await expect(events(page)).toContainText('RELEASE old read\nGET settings delivered name=initial');
  await expect(name(page)).toHaveValue('saved change');
});

test('failed add-on password submissions do not enter console diagnostics', async ({ page }) => {
  await page.getByRole('button', { name: 'Fail saves' }).click();
  await page.getByLabel('Synthetic secret', { exact: true }).fill('fixture-secret');
  await save(page).click();
  await expect(page.getByText('The add-on refused it', { exact: true })).toBeVisible();
  await expect(events(page)).toContainText('POST failed HTTP500');
  await expect(events(page)).not.toContainText('PASSWORD LOGGED');
});

test('slow add-on reads are not overlapped by subsequent poll ticks', async ({ page }) => {
  await page.getByRole('button', { name: 'Hold next poll' }).click();
  await expect(events(page)).toContainText('GET settings held name=initial');
  const before = await events(page).textContent();
  // Longer than the 2 s polling interval: the held request must retain its
  // place, rather than being replaced by a newer request before it can finish.
  await page.waitForTimeout(2200);
  await expect(events(page)).toHaveText(before!);
  await page.getByRole('button', { name: 'Release old read' }).click();
  await expect(events(page)).toContainText('RELEASE old read\nGET settings delivered name=initial');
  await expect(name(page)).toHaveValue('initial');
});
