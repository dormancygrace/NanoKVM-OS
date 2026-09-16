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

test('adaptive playout stays at 10 ms on a steady stream', () => {
  const q = new DirectPlayout('adaptive');
  for (let i = 0; i < 200; i++) {
    q.push(frame(i * 25), 100 + i * 25);
    q.take(110 + i * 25)?.close();
  }
  assert.equal(q.delayMs, 10);
  assert.equal(q.dropped, 0);
});

test('adaptive playout grows for sustained jitter and recovers slowly', () => {
  const q = new DirectPlayout('adaptive');
  for (let i = 0; i < 160; i++) {
    // Ordered arrivals: every fourth frame arrives 50 ms late with the next two.
    const arrival = 100 + i * 25 + (i % 4 === 1 ? 50 : i % 4 === 2 ? 25 : 0);
    q.push(frame(i * 25), arrival);
    q.take(arrival)?.close();
    assert.ok(q.delayMs >= 10 && q.delayMs <= 60);
    assert.ok(q.size <= 6);
  }
  assert.ok(q.delayMs >= 50);
  const raised = q.delayMs;
  for (let i = 160; i < 200; i++) {
    q.push(frame(i * 25), 100 + i * 25);
    q.take(100 + i * 25)?.close();
  }
  assert.equal(q.delayMs, raised, 'must not oscillate immediately after jitter');
  for (let i = 200; i < 1600; i++) {
    q.push(frame(i * 25), 100 + i * 25);
    q.take(100 + i * 25)?.close();
  }
  assert.ok(q.delayMs <= 12);
  q.reset();
  assert.equal(q.size, 0);
  assert.equal(q.delayMs, 10);
});

test('adaptive playout does not chase one long network outage', () => {
  const q = new DirectPlayout('adaptive');
  for (let i = 0; i < 80; i++) q.push(frame(i * 25), 100 + i * 25);
  q.push(frame(2000), 3100);
  assert.ok(q.delayMs <= 30);
  assert.ok(q.size <= 6);
  q.reset();
});


test('recurring tail bursts are covered without abrupt deadline jumps', () => {
  const q = new DirectPlayout('adaptive');
  let previous = q.delayMs;
  let lastArrival = 100;
  for (let i = 0; i < 500; i++) {
    // Three frames coalesce once per second: p90 misses this recurring tail.
    const phase = i % 50;
    const arrival = 100 + i * 20 + (phase === 20 ? 50 : phase === 21 ? 30 : phase === 22 ? 10 : 0);
    q.push(frame(i * 20), arrival);
    assert.ok(q.delayMs - previous <= Math.min(50, arrival - lastArrival) * 0.04 + 1e-6);
    previous = q.delayMs;
    lastArrival = arrival;
    q.take(arrival)?.close();
  }
  assert.ok(q.delayMs >= 50, 'recurring bursts must not be mistaken for a clean link');
  assert.ok(q.delayMs <= 60);
  q.reset();
});
