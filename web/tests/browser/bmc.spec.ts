import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const events = (page: Page) => page.getByTestId('events');
const username = (page: Page) => page.getByLabel('User name', { exact: true });
const save = (page: Page) => page.getByRole('button', { name: 'Save', exact: true });
const redfish = (page: Page) => page.getByRole('switch', { name: 'Enable Redfish' });
const complete = (page: Page) => page.getByRole('button', { name: 'Complete write', exact: true });

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/bmc.html');
  await expect(username(page)).toHaveValue('initial');
});

test('BMC switches use stored fields and serialize writes through the readback', async ({
  page
}) => {
  await username(page).fill('draft');
  await redfish(page).click();
  await expect(events(page)).toContainText('PUT held user=initial redfish=false');
  await expect(save(page)).toBeDisabled();
  await page.getByRole('tab', { name: 'IPMI over LAN' }).click();
  await expect(
    page.getByRole('switch', { name: 'Enable IPMI over LAN', exact: true })
  ).toBeDisabled();
  await page.getByRole('button', { name: 'Hold next read', exact: true }).click();
  await complete(page).click();
  await expect(events(page)).toContainText('GET held');
  await expect(save(page)).toBeDisabled();
  await page.getByRole('button', { name: 'Release read', exact: true }).click();
  await expect(save(page)).toBeEnabled();
  await expect(username(page)).toHaveValue('draft');
  await page.getByRole('switch', { name: 'Enable IPMI over LAN', exact: true }).click();
  await expect(events(page)).toContainText('PUT held user=initial redfish=false ipmi=true');
});

test('BMC save preserves edits made while the write is pending', async ({ page }) => {
  await username(page).fill('draftA');
  await save(page).click();
  await expect(events(page)).toContainText('PUT held user=draftA');
  await username(page).fill('draftB');
  await page.getByLabel('Password', { exact: true }).fill('new-fixture-secret');
  await complete(page).click();
  await expect(save(page)).toBeEnabled();
  await expect(username(page)).toHaveValue('draftB');
  await expect(page.getByLabel('Password', { exact: true })).toHaveValue('new-fixture-secret');
});

test('BMC discards a poll from before the switch mutation', async ({ page }) => {
  await page.getByRole('button', { name: 'Hold next read', exact: true }).click();
  await expect(events(page)).toContainText('GET held', { timeout: 7000 });
  await redfish(page).click();
  await complete(page).click();
  await expect(save(page)).toBeEnabled();
  await expect(redfish(page)).not.toBeChecked();
  await page.getByRole('button', { name: 'Release read', exact: true }).click();
  await expect(events(page)).toContainText('RELEASE read\nGET delivered user=initial redfish=true');
  await expect(redfish(page)).not.toBeChecked();
});

test('BMC refusal restores the switch, survives polls and redacts credentials', async ({
  page
}) => {
  await page.getByRole('button', { name: 'Fail writes', exact: true }).click();
  await redfish(page).click();
  await expect(page.getByText('fixture refused', { exact: true })).toBeVisible();
  await expect(redfish(page)).toBeChecked();
  await page.getByLabel('Password', { exact: true }).fill('fixture-secret');
  await save(page).click();
  await expect(save(page)).toBeEnabled();
  await expect(page.getByLabel('Password', { exact: true })).toHaveValue('fixture-secret');
  await expect(events(page)).not.toContainText('PASSWORD LOGGED');
  await page.waitForTimeout(5200);
  await expect(page.getByText('fixture refused', { exact: true })).toBeVisible();
});

test('BMC without a password keeps the switch local until the account is saved', async ({
  page
}) => {
  await page.goto('/tests/browser/bmc.html?no-password');
  await redfish(page).click();
  await expect(page.getByText(/Set the BMC account below/)).toBeVisible();
  await expect(events(page)).not.toContainText('PUT');
  await page.getByLabel('Password', { exact: true }).fill('fixture-secret');
  await save(page).click();
  await expect(events(page)).toContainText('PUT held user=initial redfish=true');
  await complete(page).click();
  await expect(save(page)).toBeEnabled();
  await expect(page.getByText(/Set the BMC account below/)).not.toBeVisible();
  await expect(redfish(page)).toBeChecked();
});
