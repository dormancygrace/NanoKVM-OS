import { expect, test } from './fixtures';

test.beforeEach(async ({ page }) => {
  await page.goto('/tests/browser/updates.html');
  await expect(page.getByText('NanoKVM OS is up to date', { exact: true })).toBeVisible();
});

test('core update, install and reboot each preserve their confirmation boundary', async ({
  page
}) => {
  const events = page.getByTestId('events');
  await page.getByRole('button', { name: 'Offer core update', exact: true }).click();
  await expect(page.getByRole('row').getByText('nanokvm-app', { exact: true })).toBeVisible();
  await expect(page.getByText('3.6.0-r0 → 3.6.1-r0', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Install updates', exact: true }).click();
  await expect(events).not.toContainText('post install');
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(events).not.toContainText('post install');
  await page.getByRole('button', { name: 'Install updates', exact: true }).click();
  await page.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(events).toContainText('post install {}');
  await expect(page.getByText('Installing updates…')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Check for updates', exact: true })).toBeDisabled();
  await page.getByRole('button', { name: 'Complete installation', exact: true }).click();
  await expect(
    page.getByRole('button', { name: 'Install updates', exact: true })
  ).not.toBeVisible();
  await page.getByRole('button', { name: 'Update log', exact: true }).click();
  await expect(page.getByText('Synthetic transaction completed', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Restart device', exact: true }).click();
  await expect(events).not.toContainText('post reboot');
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(events).not.toContainText('post reboot');
  await page.getByRole('button', { name: 'Restart device', exact: true }).click();
  await page.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(events).toContainText('post reboot {}');
});

test('manual package check includes non-core updates; channel and daily switch do not install', async ({
  page
}) => {
  const events = page.getByTestId('events');
  await page.getByRole('button', { name: 'Check for updates', exact: true }).click();
  await expect(page.getByText('Checking for updates…')).toBeVisible();
  await page.getByRole('button', { name: 'Complete package check', exact: true }).click();
  await expect(page.getByText('1.37.0-r0 → 1.37.0-r1', { exact: true })).toBeVisible();
  await page.getByRole('switch', { name: 'Check for updates daily', exact: true }).click();
  await expect(events).toContainText('put notice {"enabled":false}');
  await expect(
    page.getByRole('switch', { name: 'Check for updates daily', exact: true })
  ).not.toBeChecked();
  await expect(
    page.getByRole('combobox', { name: 'Update channel', exact: true })
  ).not.toBeVisible();
  await page.getByText('Advanced', { exact: true }).click();
  await page.getByRole('combobox', { name: 'Update channel', exact: true }).click();
  await page.getByText('Beta', { exact: true }).click();
  await expect(events).toContainText('post channel {"channel":"beta"}');
  await expect(events).not.toContainText('post install');
  await expect(
    page.getByRole('button', { name: 'Install updates', exact: true })
  ).not.toBeVisible();
  await expect(
    page.getByText('Beta releases may contain unfinished features or regressions.', { exact: true })
  ).toBeVisible();
});

test('failed checks retain known updates and show both package and repository errors', async ({
  page
}) => {
  await page.getByRole('button', { name: 'Offer core update', exact: true }).click();
  await page.getByRole('button', { name: 'Fail operation', exact: true }).click();
  await expect(page.getByText('Fixture APK index failed', { exact: true })).toBeVisible();
  await expect(
    page.getByText('The last check failed: Fixture repository failed', { exact: true })
  ).toBeVisible();
  await expect(page.getByRole('row').getByText('nanokvm-app', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Check for updates', exact: true })).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Install updates', exact: true })).toBeEnabled();
});

test('stable rollback requires confirmation and disables channel changes during installation', async ({
  page
}) => {
  const events = page.getByTestId('events');
  await page.getByRole('button', { name: 'Offer stable rollback', exact: true }).click();
  await page.getByText('Advanced', { exact: true }).click();
  await page.getByRole('button', { name: 'Install stable version', exact: true }).click();
  await expect(events).not.toContainText('post rollback');
  await page.getByRole('button', { name: 'OK', exact: true }).click();
  await expect(events).toContainText('post rollback {}');
  await expect(page.getByRole('combobox', { name: 'Update channel', exact: true })).toBeDisabled();
});
