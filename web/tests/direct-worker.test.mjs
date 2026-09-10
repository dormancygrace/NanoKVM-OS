import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

// Exercise the actual production worker bundle with deterministic browser API
// doubles. This does not qualify a hardware decoder or real browser playback.
const assets = new URL('../dist/assets/', import.meta.url);
const bundleName = readdirSync(assets).find((n) => /^direct\.worker-.*\.js$/.test(n));
assert.ok(bundleName, 'Build the web app before running the worker tests');
const bundle = readFileSync(new URL(bundleName, assets), 'utf8');

function harness(options = {}) {
  let now = 100;
  const frames = [],
    sockets = [],
    decoders = [],
    paints = [],
    reports = [],
    timers = new Map();
  let nextTimer = 1,
    animation;
  class Socket {
    static OPEN = 1;
    static CLOSED = 3;
    readyState = 1;
    sent = [];
    constructor(url) {
      this.url = String(url);
      sockets.push(this);
    }
    send(data) {
      this.sent.push(data);
    }
    close() {
      this.readyState = 3;
    }
  }
  class Decoder {
    state = 'unconfigured';
    decodeQueueSize = 0;
    constructor(init) {
      this.init = init;
      decoders.push(this);
    }
    configure(config) {
      this.config = config;
      if (options.rejectSoftware && config.hardwareAcceleration === 'prefer-software') {
        throw new DOMException('Software decoder unavailable', 'NotSupportedError');
      }
      this.state = 'configured';
    }
    decode(chunk) {
      if (options.throwSoftwareDecode && this.config.hardwareAcceleration === 'prefer-software') {
        throw new Error('Decode failed');
      }
      const frame = {
        timestamp: chunk.timestamp,
        displayWidth: 1920,
        displayHeight: 1080,
        closed: 0,
        close() {
          this.closed++;
        }
      };
      frames.push(frame);
      this.init.output(frame);
    }
    close() {
      this.state = 'closed';
    }
  }
  const context = {
    console,
    DOMException,
    URL,
    WebSocket: Socket,
    VideoDecoder: Decoder,
    EncodedVideoChunk: class {
      constructor(args) {
        Object.assign(this, args);
      }
    },
    ArrayBuffer,
    Uint8Array,
    DataView,
    performance: { now: () => now },
    MessageChannel: class {
      port1 = {};
      port2 = { postMessage: () => this.port1.onmessage?.() };
    },
    requestAnimationFrame: (f) => {
      animation = f;
      return 1;
    },
    cancelAnimationFrame: () => {
      animation = undefined;
    },
    setInterval: (f) => {
      const id = nextTimer++;
      timers.set(id, f);
      return id;
    },
    clearInterval: (id) => timers.delete(id),
    setTimeout: (f) => {
      const id = nextTimer++;
      timers.set(id, f);
      return id;
    },
    clearTimeout: (id) => timers.delete(id),
    postMessage(message) {
      reports.push(message);
    }
  };
  context.self = context;
  vm.runInNewContext(bundle, context);
  context.onmessage({
    data: {
      ...options,
      type: 'video',
      codec: options.codec ?? 'h265',
      url: 'wss://device.test/video',
      canvas: {
        width: 1920,
        height: 1080,
        getContext: () => ({ drawImage: (f) => paints.push(f.timestamp) })
      }
    }
  });
  const socket = sockets[0];
  socket.onopen();
  function send(timestamp, key = false) {
    const payload = new ArrayBuffer(10),
      view = new DataView(payload);
    view.setUint8(0, Number(key));
    view.setBigUint64(1, BigInt(timestamp), true);
    socket.onmessage({ data: payload });
  }
  return {
    frames,
    sockets,
    decoders,
    paints,
    reports,
    timers,
    send,
    advance: (time) => {
      now = time;
      const callback = animation;
      animation = undefined;
      callback?.();
    },
    now: (time) => {
      now = time;
    },
    stop: () => context.onmessage({ data: { type: 'stop' } })
  };
}

test('production worker preserves flow control, paces the first frame and releases resources', () => {
  const h = harness();
  assert.equal(new URL(h.sockets[0].url).searchParams.get('flow'), '8');
  h.send(0, true);
  h.advance(134);
  assert.deepEqual(h.paints, []);
  h.advance(135);
  assert.deepEqual(h.paints, [0]);
  assert.equal(h.frames[0].closed, 1);
  assert.equal(new Uint8Array(h.sockets[0].sent[0])[0], 2, 'Decode ACK remains enabled');
  h.send(17000);
  h.stop();
  assert.ok(h.frames.every((f) => f.closed === 1));
  assert.equal(h.decoders[0].state, 'closed');
  assert.equal(h.sockets[0].readyState, 3);
  assert.equal(h.timers.size, 0);
});

test('slow decoder resets once and rejects deltas until a fresh keyframe', () => {
  const h = harness();
  h.send(0, true);
  h.decoders[0].decodeQueueSize = 16;
  h.send(17000);
  h.send(34000);
  h.send(51000);
  assert.equal(h.decoders[0].state, 'closed');
  assert.equal(h.frames.length, 1);
  assert.equal(h.sockets[0].sent.filter((data) => new Uint8Array(data)[0] === 3).length, 1);
  h.send(68000, true);
  assert.equal(h.decoders.length, 2);
  assert.equal(h.frames.length, 2);
  h.stop();
  assert.ok(h.frames.every((f) => f.closed === 1));
});

test('a paused display does not retain more than six decoded frames', () => {
  const h = harness();
  for (let i = 0; i < 100; i++) {
    h.now(100 + i * 17);
    h.send(i * 17000, i === 0);
  }
  assert.equal(h.frames.filter((f) => f.closed === 0).length, 6);
  h.advance(5000);
  assert.deepEqual(h.paints, [99 * 17000]);
  assert.ok(h.frames.every((f) => f.closed === 1));
  h.stop();
});

test('codec defaults and diagnostic overrides preserve low-latency configuration', () => {
  for (const [options, expected] of [
    [{}, 'prefer-hardware'],
    [{ codec: 'h264' }, 'prefer-software'],
    [{ codec: 'h264', diagnostics: true }, 'prefer-software'],
    [{ codec: 'h264', diagnostics: true, decoderPreference: 'prefer-hardware' }, 'prefer-hardware'],
    [{ decoderPreference: 'prefer-software' }, 'prefer-hardware'],
    [{ diagnostics: true, decoderPreference: 'prefer-software' }, 'prefer-software'],
    [{ diagnostics: true, decoderPreference: 'no-preference' }, 'no-preference'],
    [{ diagnostics: true, decoderPreference: 'invalid' }, 'prefer-hardware']
  ]) {
    const h = harness(options);
    h.send(0, true);
    assert.equal(h.decoders[0].config.hardwareAcceleration, expected);
    assert.equal(h.decoders[0].config.optimizeForLatency, true);
    h.stop();
    assert.equal(h.timers.size, 0);
  }
});

test('receiver-to-decoder and receiver-to-paint measure different pipeline stages', () => {
  const h = harness({ diagnostics: true });
  h.send(0, true);
  h.advance(135);
  for (const tick of h.timers.values()) tick();
  const sample = h.reports.find((x) => x.type === 'direct-stats').stats;
  assert.equal(sample.receiveToDecodeMs.count, 1);
  assert.equal(sample.receiveToDecodeMs.p50, 0);
  assert.equal(sample.receiveToPaintMs.p50, 35);
  h.stop();
});

test('H264 unavailable software falls back once on the same keyframe', () => {
  const h = harness({ codec: 'h264', rejectSoftware: true });
  h.send(0, true);
  assert.deepEqual(
    h.decoders.map((d) => d.config.hardwareAcceleration),
    ['prefer-software', 'prefer-hardware']
  );
  assert.equal(h.decoders[0].state, 'closed');
  h.advance(135);
  assert.deepEqual(h.paints, [0]);
  assert.ok(!h.reports.some((x) => x.type === 'stream-error'));
  h.stop();
  assert.equal(h.timers.size, 0);
});

test('H264 async decoder failure switches once, resyncs and ignores stale callbacks', () => {
  const h = harness({ codec: 'h264' });
  h.send(0, true);
  const old = h.decoders[0];
  old.init.error();
  h.send(17000);
  assert.equal(h.decoders.length, 1);
  h.send(34000, true);
  assert.equal(h.decoders[1].config.hardwareAcceleration, 'prefer-hardware');
  old.init.error();
  assert.equal(h.decoders[1].state, 'configured');
  h.decoders[1].init.error();
  h.send(68000, true);
  assert.equal(h.decoders[2].config.hardwareAcceleration, 'prefer-hardware');
  assert.ok(h.frames.every((f) => f.closed === 1 || f.timestamp === 68000));
  h.stop();
  assert.equal(h.timers.size, 0);
});

test('H264 synchronous decode failure requests a keyframe using hardware fallback', () => {
  const h = harness({ codec: 'h264', throwSoftwareDecode: true });
  h.send(0, true);
  h.send(17000);
  h.send(34000, true);
  assert.equal(h.decoders.length, 2);
  assert.equal(h.decoders[1].config.hardwareAcceleration, 'prefer-hardware');
  h.advance(135);
  assert.deepEqual(h.paints, [34000]);
  h.stop();
});

test('explicit software diagnostic preference does not silently fall back', () => {
  const h = harness({ codec: 'h264', diagnostics: true, decoderPreference: 'prefer-software' });
  h.send(0, true);
  h.decoders[0].init.error();
  h.send(34000, true);
  assert.ok(h.decoders.every((d) => d.config.hardwareAcceleration === 'prefer-software'));
  h.stop();
  assert.equal(h.timers.size, 0);
});
