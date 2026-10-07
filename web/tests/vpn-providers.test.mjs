import assert from 'node:assert/strict';
import test from 'node:test';

import { summarizeVPN } from '../src/pages/desktop/menu/settings/vpn/providers.ts';

test('profile VPNs report the enabled profile', () => {
  assert.deepEqual(summarizeVPN({ available: false, profiles: [] }), {
    installed: false,
    state: 'notInstall',
    tone: 'neutral'
  });
  assert.equal(summarizeVPN({ available: true, profiles: [] }).state, 'noProfiles');
  assert.equal(
    summarizeVPN({ available: true, profiles: [{ name: 'home', state: 'off', enabled: false }] })
      .state,
    'off'
  );
  assert.deepEqual(
    summarizeVPN({
      available: true,
      profiles: [
        { name: 'home', state: 'off', enabled: false },
        { name: 'office', state: 'connected', enabled: true }
      ]
    }),
    { installed: true, state: 'connected', tone: 'success', detail: 'office' }
  );
  assert.equal(
    summarizeVPN({ available: true, profiles: [{ name: 'x', state: 'error', enabled: true }] })
      .tone,
    'danger'
  );
});

test('daemon VPNs report their state and address while running', () => {
  assert.equal(summarizeVPN({ state: 'notInstall' }).installed, false);
  assert.deepEqual(summarizeVPN({ state: 'running', ip: '100.64.0.2' }), {
    installed: true,
    state: 'running',
    tone: 'success',
    detail: '100.64.0.2'
  });
  assert.deepEqual(summarizeVPN({ state: 'notLogin', ip: '' }), {
    installed: true,
    state: 'notLogin',
    tone: 'warning',
    detail: undefined
  });
  assert.equal(summarizeVPN({ state: 'notRunning' }).tone, 'neutral');
});
