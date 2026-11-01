import os from 'node:os';
import path from 'node:path';
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests/browser',
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: 30_000,
  outputDir: path.join(process.env.RUNNER_TEMP || os.tmpdir(), 'nanokvm-browser-results'),
  reporter: 'list',
  use: { baseURL: 'http://127.0.0.1:4317', viewport: { width: 1280, height: 900 } },
  webServer: {
    command: 'pnpm browser:fixture',
    url: 'http://127.0.0.1:4317/tests/browser/recovery.html',
    reuseExistingServer: false,
    timeout: 30_000
  }
});
