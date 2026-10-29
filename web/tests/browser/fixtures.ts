import { test as base, chromium, expect } from '@playwright/test';

export { expect };
export const test = base.extend({
  context: async ({ baseURL, viewport, hasTouch, isMobile }, run, info) => {
    // Local checks use the user's existing Chrome Main tab. Do not launch a
    // substitute profile locally. CI owns a disposable Main profile.
    if (process.env.GITHUB_ACTIONS !== 'true')
      throw new Error(
        'Local browser checks require the existing Chrome Main profile; use pnpm browser:fixture and the documented manual cases.'
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
    await context.tracing.start({ screenshots: true, snapshots: true });
    try {
      await run(context);
    } finally {
      if (info.status !== info.expectedStatus)
        await context.tracing.stop({ path: info.outputPath('trace.zip') });
      await context.close();
    }
  }
});
