import test from 'node:test';
import assert from 'node:assert/strict';
import { readWebrtcCounters, summarizeWebrtc, startWebrtcDiagnostics } from '../src/pages/desktop/screen/webrtc-diagnostics.ts';

const report = (overrides = {}) => new Map([
  ['video', { id: 'video', type: 'inbound-rtp', kind: 'video', timestamp: 1000,
    codecId: 'codec', transportId: 'transport', framesDecoded: 10, framesRendered: 9,
    framesDropped: 1, framesReceived: 11, bytesReceived: 1000,
    totalDecodeTime: 0.02, jitterBufferDelay: 0.5, jitterBufferEmittedCount: 10, ...overrides }],
  ['audio', { type: 'inbound-rtp', kind: 'audio', bytesReceived: 999999 }],
  ['codec', { mimeType: 'video/H265', sdpFmtpLine: 'profile-id=1' }],
  ['transport', { selectedCandidatePairId: 'pair' }],
  ['pair', { currentRoundTripTime: 0.004, remoteCandidateId: 'private' }],
  ['private', { address: 'secret-address', usernameFragment: 'secret-credential' }]
]);

test('uses the video stream and selected transport, without exporting candidate data', () => {
  const c = readWebrtcCounters(report());
  assert.equal(c.codec, 'video/H265'); assert.equal(c.rttMs, 4);
  assert.equal(c.bytesReceived, 1000); assert.equal(c.freezeCount, null);
  assert.ok(!JSON.stringify(c).includes('secret'));
  assert.equal(readWebrtcCounters(new Map()), null);
});

test('interval rates and weighted delay use counter differences, not lifetime means', () => {
  const before = readWebrtcCounters(report());
  const now = readWebrtcCounters(report({ timestamp: 3000, framesDecoded: 70, framesRendered: 67,
    framesDropped: 3, bytesReceived: 501000, totalDecodeTime: 0.08,
    jitterBufferDelay: 2.9, jitterBufferEmittedCount: 70 }));
  const sample = summarizeWebrtc(now, before);
  assert.equal(sample.decodedFps, 30); assert.equal(sample.renderedFps, 29);
  assert.equal(sample.droppedFrames, 2); assert.equal(sample.bitrateMbps, 2);
  assert.ok(Math.abs(sample.decodeMs - 1) < 1e-8);
  assert.ok(Math.abs(sample.jitterBufferMs - 40) < 1e-8);
});

test('first sample, reset, missing and repeated timestamps never invent zero-loss or FPS', () => {
  const before = readWebrtcCounters(report());
  assert.equal(summarizeWebrtc(before, null).renderedFps, null);
  assert.equal(summarizeWebrtc(before, before).renderedFps, null);
  assert.equal(summarizeWebrtc(readWebrtcCounters(report({ id: 'new', timestamp: 2000 })), before).renderedFps, null);
  assert.equal(summarizeWebrtc(readWebrtcCounters(report({ timestamp: 2000, framesRendered: 2 })), before).renderedFps, null);
  assert.equal(summarizeWebrtc(readWebrtcCounters(report({ timestamp: 2000, framesRendered: undefined })), before).renderedFps, null);
  const zero = summarizeWebrtc(readWebrtcCounters(report({ timestamp: 2000 })), before);
  assert.equal(zero.renderedFps, 0); assert.equal(zero.decodeMs, null);
});

test('polling is non-overlapping and an in-flight reply cannot publish after disposal', async () => {
  const originalSet = globalThis.setInterval, originalClear = globalThis.clearInterval;
  let tick, resolve, calls = 0, cleared = false;
  globalThis.setInterval = (callback) => { tick = callback; return 42; };
  globalThis.clearInterval = (id) => { assert.equal(id, 42); cleared = true; };
  try {
    const peer = { getStats: () => { calls++; return new Promise(r => { resolve = r; }); } };
    const output = { dataset: { webrtcStats: 'old', webrtcHistory: 'old' }, textContent: '' };
    const stop = startWebrtcDiagnostics(peer, output);
    tick(); tick(); assert.equal(calls, 1);
    stop(); resolve(report()); await Promise.resolve(); await Promise.resolve();
    assert.equal(output.textContent, 'WebRTC diagnostics: stopped');
    assert.deepEqual(output.dataset, {}); assert.equal(cleared, true);
  } finally { globalThis.setInterval = originalSet; globalThis.clearInterval = originalClear; }
});

test('presentation comes from playback quality, independently of decoder counters', () => {
  const before = readWebrtcCounters(report({framesRendered: undefined}), {totalVideoFrames: 100, droppedVideoFrames: 10});
  const after = readWebrtcCounters(report({timestamp: 3000, framesDecoded: 70, framesRendered: undefined}), {totalVideoFrames: 160, droppedVideoFrames: 12});
  const sample = summarizeWebrtc(after, before);
  assert.equal(sample.decodedFps, 30); assert.equal(sample.presentedFps, 29);
  assert.equal(sample.renderedFps, null); assert.equal(sample.playbackDroppedFrames, 12);
});
