import assert from 'node:assert/strict';
import test from 'node:test';

import { createConnectionWarningGate } from '../src/lib/connection-warning.ts';

function harness() {
  let visible = true;
  let now = 0;
  const timers = [];
  const changes = [];
  const gate = createConnectionWarningGate((quiet) => changes.push(quiet), {
    isVisible: () => visible,
    later: (callback, ms) => {
      const timer = { at: now + ms, callback, cancelled: false };
      timers.push(timer);
      return () => {
        timer.cancelled = true;
      };
    }
  });
  return {
    gate,
    changes,
    show(value) {
      visible = value;
      gate.visibilityChanged();
    },
    advance(ms) {
      now += ms;
      for (const timer of timers.splice(0)) {
        if (timer.cancelled) continue;
        if (timer.at <= now) timer.callback();
        else timers.push(timer);
      }
    }
  };
}

test('a loss in a visible tab warns only after a second', () => {
  const h = harness();
  h.gate.lost();
  assert.equal(h.gate.isQuiet(), true);
  h.advance(999);
  assert.equal(h.gate.isQuiet(), true);
  h.advance(1);
  assert.equal(h.gate.isQuiet(), false);
  assert.deepEqual(h.changes, [true, false]);
});

test('a hidden tab never warns, and its return starts the second again', () => {
  const h = harness();
  h.show(false);
  h.gate.lost();
  h.advance(60000);
  assert.equal(h.gate.isQuiet(), true, 'quiet the whole time it is hidden');
  h.show(true);
  h.advance(500);
  assert.equal(h.gate.isQuiet(), true, 'quiet for the first second back');
  h.advance(500);
  assert.equal(h.gate.isQuiet(), false, 'a loss that outlasts it warns');
});

test('hiding a tab that shows a warning hushes it at once', () => {
  const h = harness();
  h.gate.lost();
  h.advance(1000);
  assert.equal(h.gate.isQuiet(), false);
  h.show(false);
  assert.equal(h.gate.isQuiet(), true);
  // A timer from before the tab was hidden does not undo that.
  h.advance(5000);
  assert.equal(h.gate.isQuiet(), true);
});

test('a loss again restarts the second; dispose cancels the timer', () => {
  const h = harness();
  h.gate.lost();
  h.advance(800);
  h.gate.lost();
  h.advance(800);
  assert.equal(h.gate.isQuiet(), true);
  h.advance(200);
  assert.equal(h.gate.isQuiet(), false);
  h.gate.lost();
  h.gate.dispose();
  h.advance(5000);
  assert.equal(h.gate.isQuiet(), true);
});
