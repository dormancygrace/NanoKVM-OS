import assert from 'node:assert/strict';
import test from 'node:test';

import { swapRequestSize } from '../src/lib/swap-request.ts';

test('auto zram stays auto when toggled or recompression changes', () => {
  // 52 MiB is computed, not offered: sending it would be rejected.
  assert.equal(swapRequestSize('zram', { sizeMiB: 52, auto: true }), 0);
  // A computed size that happens to be an offered one must not become manual.
  assert.equal(swapRequestSize('zram', { sizeMiB: 128, auto: true }), 0);
});

test('manual sizes are sent as they are', () => {
  assert.equal(swapRequestSize('zram', { sizeMiB: 64, auto: false }), 64);
  assert.equal(swapRequestSize('zram', { sizeMiB: 64 }), 64);
  assert.equal(swapRequestSize('sd', { sizeMiB: 256, auto: true }), 256);
});
