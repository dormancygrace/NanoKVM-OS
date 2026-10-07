import assert from 'node:assert/strict';
import test from 'node:test';

import { createLiveStatus, liveStatusInterval } from '../src/lib/live-poll.ts';

const settle = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

function fakeEnv() {
  const timers = new Set();
  return {
    timers,
    env: {
      isVisible: () => true,
      onVisibilityChange: () => () => {},
      setInterval: (callback, ms) => {
        const timer = { callback, ms };
        timers.add(timer);
        return () => timers.delete(timer);
      }
    },
    tick: () => [...timers].forEach((timer) => timer.callback())
  };
}

test('every listener shares one request and one timer', async () => {
  const { env, timers, tick } = fakeEnv();
  let reads = 0;
  const live = createLiveStatus(async () => ({ n: ++reads }), env);
  const seen = { a: [], b: [] };
  const stopA = live.subscribe((status) => seen.a.push(status?.n));
  const stopB = live.subscribe((status) => seen.b.push(status?.n));
  await settle();
  assert.equal(reads, 1);
  assert.equal(timers.size, 1);
  assert.equal([...timers][0].ms, liveStatusInterval);
  tick();
  await settle();
  assert.equal(reads, 2);
  assert.deepEqual(seen, { a: [1, 2], b: [1, 2] });

  stopA();
  assert.equal(timers.size, 1);
  stopB();
  assert.equal(timers.size, 0);
});

test('a late subscriber gets the last status without a new request', async () => {
  const { env } = fakeEnv();
  let reads = 0;
  const live = createLiveStatus(async () => ({ n: ++reads }), env);
  const stop = live.subscribe(() => {});
  await settle();
  let late;
  live.subscribe((status) => (late = status?.n));
  assert.equal(late, 1);
  assert.equal(reads, 1);
  stop();
});

test('a forced refresh during a request reads again; failures report null', async () => {
  const { env } = fakeEnv();
  const pending = [];
  const live = createLiveStatus(
    () => new Promise((resolve, reject) => pending.push({ resolve, reject })),
    env
  );
  const seen = [];
  live.subscribe((status) => seen.push(status));
  assert.equal(pending.length, 1);
  void live.refresh(); // joins the request on its way
  void live.refresh({ force: true }); // state changed: read once more after it
  pending[0].resolve({ n: 'old' });
  await settle();
  assert.equal(pending.length, 2);
  pending[1].reject(new Error('offline'));
  await settle();
  assert.deepEqual(seen, [{ n: 'old' }, null]);
  assert.equal(pending.length, 2);
});
