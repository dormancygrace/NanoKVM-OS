import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const events = (page: Page) => page.getByTestId('events');
const dialog = (page: Page) => page.getByRole('dialog');
const admin = (page: Page) => dialog(page).getByLabel('Current administrator password');
const previewDialog = (page: Page) =>
  page.getByRole('dialog', { name: 'Preview changes', exact: true });

test('recovery export validates its own password and clears cancelled administrator input', async ({
  page
}) => {
  await page.goto('/tests/browser/recovery.html');
  const download = page.getByRole('button', { name: 'Download encrypted backup', exact: true });
  await expect(download).toBeDisabled();
  await page.getByLabel('Backup password', { exact: true }).fill('short');
  await expect(page.getByText('At least 12 characters.', { exact: true })).toBeVisible();
  await page.getByLabel('Backup password', { exact: true }).fill('fixture-export-only');
  await download.click();
  await admin(page).fill('unused');
  await dialog(page).getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(dialog(page)).not.toBeVisible();
  await download.click();
  await expect(admin(page)).toHaveValue('');
  await admin(page).fill('fixture-admin-only');
  await admin(page).press('Enter');
  await expect(events(page)).toContainText('export password=true');
  await expect(events(page)).toContainText('download name=NanoKVM-settings.nkos blob=true');
  await expect(events(page)).toContainText('export admin=true sensitive=true');
  await expect(dialog(page)).not.toBeVisible();
});

test('recovery restore uses its own archive password and applies exactly the confirmed snapshot', async ({
  page
}) => {
  await page.goto('/tests/browser/recovery.html?failure=apply');
  await page.getByRole('button', { name: 'Select fixture archive', exact: true }).click();
  await page.getByLabel('Archive password', { exact: true }).fill('fixture-archive-only');
  await page.getByRole('button', { name: 'Preview changes', exact: true }).first().click();
  await admin(page).fill('wrong');
  await admin(page).press('Enter');
  await expect(
    dialog(page).getByText('Wrong administrator password', { exact: true })
  ).toBeVisible();
  await admin(page).fill('fixture-admin-only');
  await admin(page).press('Enter');
  await expect(previewDialog(page).getByText('settings/example', { exact: true })).toBeVisible();
  const apply = previewDialog(page).getByRole('button', { name: 'Apply and reboot', exact: true });
  await expect(apply).toBeDisabled();
  await previewDialog(page).getByRole('checkbox').check();
  await apply.click();
  await expect(
    previewDialog(page).getByText('Fixture apply refused', { exact: true })
  ).toBeVisible();
  await apply.click();
  await expect(events(page)).toContainText('"action":"restore","mode":"migrate"');
  await expect(events(page)).toContainText('"archivePassword":true,"archive":true');
  await expect(events(page)).toContainText('apply confirmed snapshot=true');
  await expect(
    page.getByRole('button', { name: 'Reboot to finish recovery', exact: true })
  ).toBeVisible();
});

test('recovery reset preserves selected keep flags and pending reboot asks for fresh authentication', async ({
  page
}) => {
  await page.goto('/tests/browser/recovery.html');
  await page
    .getByRole('checkbox', { name: 'Keep users, passwords and authorized SSH keys', exact: true })
    .uncheck();
  await page
    .getByRole('checkbox', { name: 'Keep all installed apps, including APK packages', exact: true })
    .uncheck();
  await page.getByRole('button', { name: 'Preview changes', exact: true }).last().click();
  await admin(page).fill('fixture-admin-only');
  await admin(page).press('Enter');
  await expect(events(page)).toContainText(
    '"action":"reset","mode":"migrate","keepUsers":false,"keepApps":false'
  );
  await expect(events(page)).toContainText('"archivePassword":false,"archive":false');
  await previewDialog(page).getByRole('checkbox').check();
  await previewDialog(page).getByRole('button', { name: 'Apply and reboot', exact: true }).click();
  await expect(previewDialog(page)).not.toBeVisible();
  await page.getByRole('button', { name: 'Reboot to finish recovery', exact: true }).click();
  await expect(admin(page)).toHaveValue('');
  await admin(page).fill('fixture-admin-only');
  await admin(page).press('Enter');
  await expect(events(page)).toContainText('reboot admin=true sensitive=true');
});

test('recovery rejects an oversized archive before a preview request', async ({ page }) => {
  await page.goto('/tests/browser/recovery.html');
  await page.getByRole('button', { name: 'Select oversized archive', exact: true }).click();
  await page.getByLabel('Archive password', { exact: true }).fill('fixture-archive-only');
  await page.getByRole('button', { name: 'Preview changes', exact: true }).first().click();
  await admin(page).fill('fixture-admin-only');
  await admin(page).press('Enter');
  await expect(
    dialog(page).getByText('Choose a settings archive up to 4 MiB.', { exact: true })
  ).toBeVisible();
  await expect(events(page)).not.toContainText('preview');
});
