import { test as base, chromium, expect } from '@playwright/test';

export { expect };
export const test = base.extend({
  context: async ({ baseURL, viewport, hasTouch, isMobile }, run, info) => {
    // Local checks use the user's existing Chrome Main tab. Do not launch a
    // substitute profile locally. CI owns a disposable Main profile.
    if (process.env.GITHUB_ACTIONS !== 'true')
      throw new Error(
        'Local browser checks require the existing Chrome Main profile; use pnpm browser:fixture and open /tests/browser/recovery.html in it.'
      );
    const context = await chromium.launchPersistentContext(info.outputPath('chrome-profile'), {
      channel: 'chrome',
      headless: true,
      args: ['--profile-directory=Main'],
      baseURL,
      viewport,
      hasTouch,
      isMobile
    });
    const pageErrors: string[] = [];
    context.on('page', (page) => page.on('pageerror', (error) => pageErrors.push(error.message)));
    await context.tracing.start({ screenshots: true, snapshots: true });
    let failed = false;
    try {
      await run(context);
      expect(pageErrors, 'uncaught browser errors').toEqual([]);
    } catch (error) {
      failed = true;
      throw error;
    } finally {
      if (failed || info.status !== info.expectedStatus)
        await context.tracing.stop({ path: info.outputPath('trace.zip') });
      await context.close();
    }
  }
});
