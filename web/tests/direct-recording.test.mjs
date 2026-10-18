import assert from 'node:assert/strict';
import test from 'node:test';

import { PacketTimeline, parameterSets } from '../src/lib/direct-recording.ts';

const frame = (timestamp, key = false, width = 1920, height = 1080) => ({
  key,
  timestamp,
  width,
  height,
  data: new Uint8Array([timestamp & 0xff])
});

test('a track starts at the first keyframe with a known size and counts from zero', () => {
  const timeline = new PacketTimeline();
  assert.equal(timeline.push(frame(1000)), null);
  assert.equal(timeline.push(frame(2000, true, 0, 0)), null);
  assert.equal(timeline.started, false);
  assert.equal(timeline.push(frame(5_000_000, true)), null);
  assert.equal(timeline.started, true);
  const first = timeline.push(frame(5_011_111));
  assert.equal(first.key, true);
  assert.equal(first.seconds, 0);
  assert.ok(Math.abs(first.duration - 0.011111) < 1e-9);
  const last = timeline.flush();
  assert.equal(last.key, false);
  assert.ok(Math.abs(last.seconds - 0.011111) < 1e-9);
  assert.ok(Math.abs(last.duration - 0.011111) < 1e-9);
  assert.equal(timeline.flush(), null);
});

test('frames that go back in time are dropped and counted', () => {
  const timeline = new PacketTimeline();
  timeline.push(frame(0, true));
  timeline.push(frame(10_000));
  assert.equal(timeline.push(frame(10_000)), null);
  assert.equal(timeline.push(frame(5_000)), null);
  assert.equal(timeline.dropped, 2);
  assert.equal(timeline.push(frame(20_000)).seconds, 0.01);
});

test('a new picture size ends the track', () => {
  const timeline = new PacketTimeline();
  timeline.push(frame(0, true));
  assert.equal(timeline.push(frame(10_000, true, 2560, 1440)), 'size-changed');
});

// H.265 keyframe: VPS, SPS, PPS and an IDR slice, with 4- and 3-byte start codes.
const hevcKey = (sps) =>
  new Uint8Array([0, 0, 0, 1, 0x40, 1, 9, 0, 0, 0, 1, 0x42, 1, sps, 0, 0, 1, 0x44, 1, 7, 0, 0, 1, 0x26, 1, 0xaf]);

test('parameter sets are the VPS, SPS and PPS of a keyframe', () => {
  assert.equal(parameterSets(hevcKey(5), 'h265'), '40,1,9|42,1,5|44,1,7');
  assert.equal(parameterSets(new Uint8Array([0, 0, 1, 0x02, 1, 2]), 'h265'), '');
  // H.264: SPS 7 and PPS 8, not the IDR slice 5.
  assert.equal(parameterSets(new Uint8Array([0, 0, 0, 1, 0x67, 1, 0, 0, 1, 0x68, 2, 0, 0, 1, 0x65, 3]), 'h264'), '67,1|68,2');
});

test('a keyframe with other parameter sets ends the track before it', () => {
  const timeline = new PacketTimeline('h265');
  const key = (ts, sps) => ({ key: true, timestamp: ts, width: 1920, height: 1080, data: hevcKey(sps) });
  timeline.push(key(0, 5));
  assert.equal(timeline.push(frame(10_000)).key, true);
  // The same parameter sets again (a regular IDR) continue the track.
  assert.equal(timeline.push(key(20_000, 5)).key, false);
  assert.equal(timeline.push(key(30_000, 6)), 'size-changed');
  assert.equal(timeline.flush().seconds, 0.02);
});
