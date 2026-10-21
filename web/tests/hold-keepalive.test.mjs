import assert from 'node:assert/strict';
import test from 'node:test';

import { heldBy, HOLD_KEEPALIVE_MS, HoldKeepalive } from '../src/lib/hold-keepalive.ts';

test('all HID report shapes distinguish held keys, buttons and motion', () => {
  assert.deepEqual(heldBy([1, 2, 0, 0, 0, 0, 0, 0, 0]), { kind: 'keyboard', held: true });
  assert.deepEqual(heldBy([1, 0, 0, 4, 0, 0, 0, 0, 0]), { kind: 'keyboard', held: true });
  assert.deepEqual(heldBy([1, 0, 0, 0, 0, 0, 0, 0, 0]), { kind: 'keyboard', held: false });
  for (const length of [4, 5, 6, 7]) {
    const report = [2, 0, ...Array(length - 1).fill(1)];
    const kind = length < 6 ? 'relative' : 'absolute';
    assert.deepEqual(heldBy(report), { kind, held: false });
    report[1] = 1;
    assert.deepEqual(heldBy(report), { kind, held: true });
  }
  for (const invalid of [[], [0], [4], [1, 1], [2, 1, 2]]) assert.equal(heldBy(invalid), null);
});

test('one timer while either input is held, none at idle or after reconnect', () => {
  let callback,
    starts = 0,
    stops = 0,
    sent = 0;
  const watcher = new HoldKeepalive(() => sent++, {
    setInterval(cb, ms) {
      assert.equal(ms, HOLD_KEEPALIVE_MS);
      callback = cb;
      starts++;
      return starts;
    },
    clearInterval() {
      stops++;
      callback = undefined;
    }
  });
  assert.equal(watcher.active, false);
  watcher.observe([2, 0, 1, 0, 0]);
  assert.equal(starts, 0);
  watcher.observe([1, 0, 0, 4, 0, 0, 0, 0, 0]);
  watcher.observe([2, 1, 0, 0, 0]);
  assert.equal(starts, 1);
  callback();
  assert.equal(sent, 1);
  watcher.observe([1, 0, 0, 0, 0, 0, 0, 0, 0]);
  assert.equal(stops, 0);
  watcher.observe([2, 0, 0, 0, 0]);
  assert.equal(stops, 1);
  watcher.observe([2, 1, 0, 0, 0]);
  watcher.reset();
  assert.equal(stops, 2);
  assert.equal(watcher.active, false);
});
