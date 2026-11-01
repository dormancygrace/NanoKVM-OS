import type { Page } from '@playwright/test';

import { expect, test } from './fixtures';

const events = (page: Page) => page.getByTestId('events');
const dialog = (page: Page) => page.getByRole('dialog');
const admin = (page: Page) => dialog(page).getByLabel('Current administrator password');
const previewDialog = (page: Page) =>
  page.getByRole('dialog', { name: 'Preview changes', exact: true });

// Security boundary: the administrator password is asked again for every action.

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
