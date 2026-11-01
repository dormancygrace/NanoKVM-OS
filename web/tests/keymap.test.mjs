import assert from 'node:assert/strict';
import test from 'node:test';

import { getKeycode } from '../src/lib/keymap.ts';

test('media keys are not sent as keyboard usages', () => {
  for (const name of ['MediaPlayPause', 'BrowserSearch', 'LaunchApp2', 'Sleep', 'Wake']) {
    assert.equal(getKeycode(name), undefined, name);
  }
  assert.equal(getKeycode('AudioVolumeUp'), 0x80);
  assert.equal(getKeycode('Power'), 0x66);
});
