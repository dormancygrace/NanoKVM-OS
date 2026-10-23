import assert from 'node:assert/strict';
import test from 'node:test';

import { virtualDiskFromLive } from '../src/lib/virtual-disk-state.ts';

const live = (usb) => ({ usb });

test('a failed poll keeps the known state', () => {
  assert.equal(virtualDiskFromLive(true, null), true);
  assert.equal(virtualDiskFromLive(false, null), false);
  assert.equal(virtualDiskFromLive(null, null), null);
});

test('a live status decides', () => {
  assert.equal(virtualDiskFromLive(null, live({ disk: true, mode: 'normal' })), true);
  assert.equal(virtualDiskFromLive(true, live({ disk: false })), false);
  assert.equal(virtualDiskFromLive(true, live({ disk: true, mode: 'hid-only' })), false);
  // No usb part: not an administrator, nothing to show.
  assert.equal(virtualDiskFromLive(true, live(null)), null);
});
