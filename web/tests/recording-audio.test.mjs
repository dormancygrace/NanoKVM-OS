import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import test from 'node:test';
import vm from 'node:vm';

import {
  AUDIO_RESYNC_SECONDS,
  AudioTimeline,
  getRecordingAudio,
  OPUS_FRAME_SECONDS,
  OPUS_SILENCE,
  registerRecordingAudio
} from '../src/lib/recording-audio.ts';
import { PacketTimeline, RtpClock } from '../src/lib/recording-source.ts';
import { publishUsbAudioStream } from '../src/lib/usb-audio-stream.ts';

test('audio source binds to the listened track and detaches when speakers stop', () => {
  const track = { readyState: 'live' };
  const stream = { getAudioTracks: () => [track] };
  let stopped = 0;
  const ends = [];
  const unregister = registerRecordingAudio(track, {
    start() {
      return () => stopped++;
    }
  });
  publishUsbAudioStream(stream);
  const source = getRecordingAudio(stream);
  const stop = source.start(
    () => {},
    (reason) => ends.push(reason)
  );
  publishUsbAudioStream(null);
  stop();
  assert.equal(stopped, 1);
  assert.deepEqual(ends, ['stopped']);
  unregister();
  assert.equal(getRecordingAudio(stream), null);
});
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
test('audio worker forwards the original while recording a byte-identical copy', async () => {
  const code = stripTypeScriptTypes(
    readFileSync(new URL('../src/lib/audio-record.worker.ts', import.meta.url), 'utf8')
  ).replace(/^export .*$/gm, '');
  const messages = [],
    forwarded = [];
  const self = { postMessage: (m) => messages.push(m) };
  vm.runInNewContext(code, { self, TransformStream, performance });
  self.onmessage({ data: { recording: 2 } });
  const data = new Uint8Array([0xfc, 0xff, 0xfe]).buffer;
  const frame = { data, timestamp: 48, getMetadata: () => ({ rtpTimestamp: 48 }) };
  self.onrtctransform({
    transformer: {
      readable: new ReadableStream({
        start(c) {
          c.enqueue(frame);
          c.close();
        }
      }),
      writable: new WritableStream({ write: (f) => forwarded.push(f) })
    }
  });
  await new Promise((r) => setImmediate(r));
  await new Promise((r) => setImmediate(r));
  assert.equal(forwarded[0], frame);
  assert.equal(messages[0].recording, 2);
  assert.notEqual(messages[0].data, data);
  assert.deepEqual(new Uint8Array(messages[0].data), new Uint8Array(data));
});

function receiverFixture(options = {}) {
  const workers = [];
  const sources = new Map();
  let failures = 0;
  class Worker {
    messages = [];
    terminated = false;
    constructor() {
      workers.push(this);
    }
    postMessage(m) {
      this.messages.push(m);
    }
    terminate() {
      this.terminated = true;
    }
  }
  class Transform {
    constructor() {
      if (options.denied) throw Error('denied');
    }
  }
  const code = stripTypeScriptTypes(
    readFileSync(new URL('../src/lib/webrtc-audio-recording.ts', import.meta.url), 'utf8')
  )
    .replace(/^import[\s\S]*?from\s+['"][^'"]+['"];\s*/gm, '')
    .replace(/^export /gm, '');
  const attach = vm.runInNewContext('(function(){' + code + ';return recordAudioReceiver})()', {
    AudioWorker: Worker,
    RTCRtpScriptTransform: Transform,
    RtpClock,
    Uint8Array,
    registerRecordingAudio(track, source) {
      sources.set(track, source);
      return () => sources.delete(track);
    }
  });
  const track = {};
  const owner = attach({ track }, () => failures++);
  return { owner, track, workers, sources, attach, failures: () => failures };
}
test('audio receiver isolates sessions, releases registration and falls back after worker failure', () => {
  const f = receiverFixture(),
    seen = [],
    ends = [];
  const source = f.sources.get(f.track);
  source.start(
    () => assert.fail('stale packet'),
    () => {}
  )();
  source.start(
    (p) => seen.push(p),
    (r) => ends.push(r)
  );
  const w = f.workers[0];
  const frame = { rtpTimestamp: 1, receivedAt: 1000, data: new Uint8Array([1]).buffer };
  w.onmessage({ data: { ...frame, recording: 1 } });
  w.onmessage({ data: { ...frame, recording: 2 } });
  assert.equal(seen.length, 1);
  assert.equal(seen[0].timestamp, 0);
  w.onerror();
  w.onerror();
  assert.equal(f.failures(), 1);
  assert.deepEqual(ends, ['stream-error']);
  assert.equal(f.sources.size, 0);
  assert.equal(
    f.attach({ track: {} }, () => {}),
    null
  );
  f.owner.close();
  assert.ok(w.terminated);
});
test('denied audio transform leaves playback unmodified and releases the worker', () => {
  const f = receiverFixture({ denied: true });
  assert.equal(f.owner, null);
  assert.ok(f.workers[0].terminated);
  assert.equal(f.sources.size, 0);
});

test('fallback audio survives temporary mute and closes on an actual end', async () => {
  let closes = 0;
  let bitrate;
  class Sound {
    errorPromise = new Promise(() => {});
    constructor(_track, options) {
      bitrate = options.bitrate;
    }
    close() {
      closes++;
    }
  }
  class Output {
    state = 'pending';
    addVideoTrack() {}
    addAudioTrack() {}
    async start() {
      this.state = 'started';
    }
    async finalize() {}
    async cancel() {}
  }
  const code = stripTypeScriptTypes(
    readFileSync(new URL('../src/lib/passthrough-recording.ts', import.meta.url), 'utf8')
  )
    .replace(/^import[\s\S]*?from\s+['"][^'"]+['"];\s*/gm, '')
    .replace(/^export /gm, '');
  const record = vm.runInNewContext('(function(){' + code + ';return recordPassthrough})()', {
    WritableStream,
    Error,
    Promise,
    PacketTimeline,
    AudioTimeline,
    AUDIO_RESYNC_SECONDS,
    OPUS_FRAME_SECONDS,
    OPUS_SILENCE,
    DIRECT_H264_CODEC: 'avc1.4d0029',
    DIRECT_H265_CODEC: 'hev1.1.6.L150.B0',
    canEncodeAudio: async () => true,
    MediaStreamAudioTrackSource: Sound,
    Output,
    Mp4OutputFormat: class {},
    StreamTarget: class {},
    EncodedVideoPacketSource: class {
      async add() {}
    },
    EncodedPacket: class {}
  });
  const track = new EventTarget();
  track.readyState = 'live';
  track.stop = () => {};
  let packet;
  const session = await record(
    {
      codec: 'h264',
      start(p) {
        packet = p;
        return () => {};
      }
    },
    new WritableStream(),
    track
  );
  packet({ key: true, timestamp: 0, width: 160, height: 90, data: new Uint8Array([1]) });
  await session.started;
  track.dispatchEvent(new Event('mute'));
  track.dispatchEvent(new Event('unmute'));
  assert.equal(closes, 0);
  assert.equal(bitrate, 192000);
  track.dispatchEvent(new Event('ended'));
  assert.equal(closes, 1);
  session.stop();
  await session.done;
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
