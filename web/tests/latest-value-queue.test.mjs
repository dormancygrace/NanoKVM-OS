import assert from 'node:assert/strict';
import test from 'node:test';

import { LatestValueQueue } from '../src/lib/latest-value-queue.ts';

const deferred = () => {
  let resolve;
  const promise = new Promise((done) => {
    resolve = done;
  });
  return { promise, resolve };
};

test('a fast off-to-on change is delivered after the in-flight write', async () => {
  const first = deferred();
  const writes = [];
  const queue = new LatestValueQueue(async (value) => {
    writes.push(value);
    if (writes.length === 1) await first.promise;
  });

  const off = queue.enqueue(-1);
  const on = queue.enqueue(60);
  assert.deepEqual(writes, [-1]);

  first.resolve();
  await Promise.all([off, on]);
  assert.deepEqual(writes, [-1, 60]);
});

test('multiple waiting changes coalesce to the latest requested state', async () => {
  const first = deferred();
  const writes = [];
  const queue = new LatestValueQueue(async (value) => {
    writes.push(value);
    if (writes.length === 1) await first.promise;
  });

  const done = queue.enqueue(-1);
  queue.enqueue(15);
  queue.enqueue(30);
  queue.enqueue(60);
  first.resolve();
  await done;

  assert.deepEqual(writes, [-1, 60]);
});
