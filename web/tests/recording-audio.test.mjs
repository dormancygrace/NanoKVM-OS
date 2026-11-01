import assert from 'node:assert/strict';
import test from 'node:test';
import vm from 'node:vm';

import { AudioTimeline } from '../src/lib/recording-audio.ts';
import { RtpClock } from '../src/lib/recording-source.ts';

test('audio timeline retains RTP gaps and ignores pre-video or duplicate packets', () => {
  const t = new AudioTimeline(1000);
  const p = (timestamp, receivedAt) => ({ timestamp, receivedAt });
  assert.equal(t.push(p(0, 980)), null);
  assert.equal(t.push(p(20000, 1000)), 0);
  assert.equal(t.push(p(220000, 1200)), 0.2);
  assert.equal(t.push(p(220000, 1300)), null);
  const clock = new RtpClock(48000);
  assert.equal(clock.microseconds(2 ** 32 - 480), 0);
  assert.equal(clock.microseconds(480), 20000);
});
test('audio timeline re-anchors when the sender paused and fills silence up to a time', () => {
  const timeline = new AudioTimeline(1000);
  assert.equal(timeline.push({ timestamp: 0, receivedAt: 1000 }), 0);
  assert.deepEqual(
    timeline.silence(0.05).map((s) => Math.round(s * 1000)),
    [20, 40]
  );
  // late by less than the threshold: RTP time is kept, inside the silence
  assert.equal(timeline.push({ timestamp: 20000, receivedAt: 1300 }), null);
  // the sender paused for 5 s: arrival time wins
  assert.equal(timeline.push({ timestamp: 40000, receivedAt: 6000 }), 5);
  assert.ok(Math.abs(timeline.push({ timestamp: 60000, receivedAt: 6020 }) - 5.02) < 1e-9);
  assert.deepEqual(timeline.silence(5.02), []);
});

// RTP offsets emitted by the server pause-gap path: repeated capture pauses,
// three dropped packets, then a reset capture index. Network delay varies but
// stays below the fallback re-anchor threshold.
test('server-corrected RTP gaps survive silence fill without a second time jump', () => {
  const clock = new RtpClock(48000);
  const timeline = new AudioTimeline(1000);
  const base = 2 ** 32 - 960;
  for (const [seconds, delay] of [
    [0, 0],
    [0.02, 0],
    [5.02, 0.2],
    [5.1, 0.1],
    [8.1, 0.2],
    [10.1, 0.1]
  ]) {
    timeline.silence(seconds - 1.5);
    const actual = timeline.push({
      timestamp: clock.microseconds((base + Math.round(seconds * 48000)) >>> 0),
      receivedAt: 1000 + (seconds + delay) * 1000
    });
    assert.notEqual(actual, null, 'resumed audio must not be discarded as synthesized silence');
    assert.ok(Math.abs(actual - seconds) < 1e-6, `${actual} instead of ${seconds}`);
  }
});
