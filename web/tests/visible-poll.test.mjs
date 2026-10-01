import assert from 'node:assert/strict';
import test from 'node:test';

import { pollWhileVisible } from '../src/lib/visible-poll.ts';

function environment(visible = true) {
  let timer, listener;
  let timerStops = 0,
    listenerStops = 0;
  return {
    env: {
      isVisible: () => visible,
      setInterval: (callback, ms) => {
        assert.equal(ms, 3000);
        timer = callback;
        return () => timerStops++;
      },
      onVisibilityChange: (callback) => {
        listener = callback;
        return () => listenerStops++;
      }
    },
    tick: () => timer(),
    change: (next) => {
      visible = next;
      listener();
    },
    stops: () => [timerStops, listenerStops]
  };
}

test('hidden tabs skip passive reads and resume with an immediate refresh', () => {
  const page = environment();
  let reads = 0;
  const stop = pollWhileVisible(() => reads++, 3000, page.env);
  assert.equal(reads, 0); // The caller owns initial fetch.
  page.tick();
  assert.equal(reads, 1);
  page.change(false);
  page.tick();
  page.tick();
  assert.equal(reads, 1);
  page.change(true);
  assert.equal(reads, 2);
  page.tick();
  assert.equal(reads, 3);
  stop();
});

test('a poller mounted in the background does not fetch until visible', () => {
  const page = environment(false);
  let reads = 0;
  const stop = pollWhileVisible(() => reads++, 3000, page.env);
  page.tick();
  assert.equal(reads, 0);
  page.change(true);
  assert.equal(reads, 1);
  stop();
});

test('cleanup cancels timer and listener once and ignores already queued callbacks', () => {
  const page = environment();
  let reads = 0;
  const stop = pollWhileVisible(() => reads++, 3000, page.env);
  stop();
  stop();
  page.tick();
  page.change(true);
  assert.equal(reads, 0);
  assert.deepEqual(page.stops(), [1, 1]);
});

test('each consumer keeps its own polling lifecycle', () => {
  const first = environment();
  const second = environment();
  let firstReads = 0,
    secondReads = 0;
  const stopFirst = pollWhileVisible(() => firstReads++, 3000, first.env);
  const stopSecond = pollWhileVisible(() => secondReads++, 3000, second.env);
  stopFirst();
  first.tick();
  second.tick();
  assert.equal(firstReads, 0);
  assert.equal(secondReads, 1);
  stopSecond();
});
