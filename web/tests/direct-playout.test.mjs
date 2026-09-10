import assert from 'node:assert/strict';
import test from 'node:test';

import { DirectPlayout } from '../src/pages/desktop/screen/direct-playout.ts';

const frame = (ms) => ({
  timestamp: ms * 1000,
  closed: 0,
  close() {
    this.closed++;
  }
});

test('a burst is paced by source time with bounded receiver delay', () => {
  const q = new DirectPlayout(35, 6);
  const a = frame(0),
    b = frame(17),
    c = frame(34);
  q.push(a, 100);
  q.push(b, 125);
  q.push(c, 135);
  assert.equal(q.take(134), undefined);
  assert.equal(q.take(135), a);
  assert.equal(q.take(151), undefined);
  assert.equal(q.take(152), b);
  assert.equal(q.take(169), c);
  assert.equal(q.size, 0);
  assert.equal(q.dropped, 0);
});

test('late renderer closes superseded frames and shows newest due frame', () => {
  const q = new DirectPlayout(35, 6);
  const a = frame(0),
    b = frame(17),
    c = frame(34),
    d = frame(100);
  q.push(a, 100);
  q.push(b, 117);
  q.push(c, 134);
  q.push(d, 200);
  assert.equal(q.take(200), c);
  assert.equal(a.closed, 1);
  assert.equal(b.closed, 1);
  assert.equal(c.closed, 0);
  assert.equal(q.size, 1);
  assert.equal(q.dropped, 2);
  q.reset();
  assert.equal(d.closed, 1);
  assert.equal(q.size, 0);
});

test('background tab cannot retain an unbounded GPU frame queue', () => {
  const q = new DirectPlayout(35, 6);
  const frames = Array.from({ length: 100 }, (_, i) => frame(i * 17));
  for (const f of frames) q.push(f, f.timestamp / 1000 + 100);
  assert.equal(q.size, 6);
  assert.equal(q.dropped, 94);
  assert.equal(q.take(5000), frames[99]);
  assert.ok(frames.slice(0, -1).every((f) => f.closed === 1));
  assert.equal(frames[99].closed, 0);
});

test('sender timestamp restart releases old frames and starts a new epoch', () => {
  const q = new DirectPlayout(35);
  const old = frame(10000),
    fresh = frame(0);
  q.push(old, 100);
  q.push(fresh, 120);
  assert.equal(old.closed, 1);
  assert.equal(q.take(154), undefined);
  assert.equal(q.take(155), fresh);
});
