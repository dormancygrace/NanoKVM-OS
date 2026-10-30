import { expect, test } from './fixtures';

test('shutdown defaults to Cancel; only explicit confirmation prepares it', async ({ page }) => {
  await page.goto('/tests/browser/shutdown.html');
  const trigger = page.getByRole('button', { name: 'Shut down NanoKVM', exact: true });
  await trigger.click();
  const dialog = page.getByRole('dialog');
  const cancel = dialog.getByRole('button', { name: 'No', exact: true });
  const confirm = dialog.getByRole('button', { name: 'Shut down NanoKVM', exact: true });
  await expect(cancel).toBeFocused();
  await expect(cancel).toHaveClass(/ant-btn-primary/);
  await expect(confirm).not.toHaveClass(/ant-btn-primary/);
  await page.keyboard.press('Enter');
  await expect(dialog).toHaveCount(0);
  await expect(page.getByTestId('events')).toHaveText('[]');

  await trigger.click();
  await confirm.click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByTestId('events')).toHaveText(
    JSON.stringify([
      {
        method: 'post',
        url: '/api/vm/system/shutdown',
        body: { action: 'prepare', magicWake: false }
      }
    ])
  );
  await expect(
    page.getByText('The request failed. Check the connection and try again.', { exact: true })
  ).toBeVisible();
});
