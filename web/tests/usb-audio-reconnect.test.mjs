import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

import * as reconnect from '../src/lib/reconnect-delay.ts';

// useUsbAudio with a small hook renderer, fake time, and fake WebSocket and
// RTCPeerConnection, so reconnects can be counted and timed as the page
// would see them.
function harness() {
  let now = 0;
  let nextTimer = 0;
  const timers = new Map();
  const schedule = (fn, delay, every) => {
    const id = ++nextTimer;
    timers.set(id, { fn, at: now + delay, every });
    return id;
  };
  const clear = (id) => timers.delete(id);

  // Hooks: state and refs by slot, effects run after each render when their
  // dependencies change; a state change renders again.
  const slots = [];
  const effects = [];
  let slot = 0;
  let dirty = false;
  const changed = (a, b) =>
    !a || !b || a.length !== b.length || a.some((v, i) => !Object.is(v, b[i]));
  const react = {
    useState(initial) {
      const i = slot++;
      if (!(i in slots)) slots[i] = typeof initial === 'function' ? initial() : initial;
      return [
        slots[i],
        (value) => {
          const next = typeof value === 'function' ? value(slots[i]) : value;
          if (!Object.is(next, slots[i])) {
            slots[i] = next;
            dirty = true;
          }
        }
      ];
    },
    useRef(initial) {
      const i = slot++;
      if (!(i in slots)) slots[i] = { current: initial };
      return slots[i];
    },
    useCallback(fn, deps) {
      const i = slot++;
      if (!(i in slots) || changed(slots[i].deps, deps)) slots[i] = { fn, deps };
      return slots[i].fn;
    },
    useEffect(fn, deps) {
      const i = slot++;
      const old = effects[i];
      if (!old || changed(old.deps, deps))
        effects[i] = { fn, deps, pending: true, cleanup: old?.cleanup };
    }
  };

  const sockets = [];
  class FakeSocket {
    static OPEN = 1;
    readyState = 1;
    sent = [];
    constructor(url) {
      this.url = url;
      this.created = now;
      sockets.push(this);
    }
    send(data) {
      this.sent.push(JSON.parse(data));
    }
    close() {
      this.readyState = 3;
    }
  }
  const peers = [];
  class FakePeer {
    connectionState = 'new';
    packets = 0;
    constructor() {
      peers.push(this);
    }
    addTransceiver() {
      return { receiver: {} };
    }
    async createOffer() {
      return { sdp: 'v=0' };
    }
    async setLocalDescription(description) {
      this.localDescription = description;
    }
    async setRemoteDescription() {}
    async addIceCandidate() {}
    async getStats() {
      const reports = [{ type: 'inbound-rtp', kind: 'audio', packetsReceived: this.packets }];
      return { forEach: (fn) => reports.forEach(fn) };
    }
    close() {
      this.connectionState = 'closed';
    }
    connect() {
      this.connectionState = 'connected';
      this.onconnectionstatechange?.();
    }
  }

  const output = {
    context: {
      state: 'running',
      createMediaStreamSource: () => ({ connect() {}, disconnect() {} })
    },
    gain: { gain: { value: 1 } },
    resume: async () => {},
    close() {}
  };
  const modules = {
    react,
    '@/api/virtual-device.ts': { usbCompositionChangedEvent: 'composition' },
    '@/lib/audio-output.ts': { createAudioOutput: () => output },
    '@/lib/audio-sdp.ts': { stereoOffer: (sdp) => sdp },
    '@/lib/live-status.ts': {
      refreshLiveStatus() {},
      subscribeLiveStatus: (listener) => {
        listener({ audio: { audio: true } });
        return () => {};
      }
    },
    '@/lib/reconnect-delay.ts': reconnect,
    '@/lib/usb-audio-stream.ts': { publishUsbAudioStream() {} },
    '@/lib/video-audio.ts': { getVideoAudio: () => null, watchVideoAudio: () => () => {} },
    '@/lib/webrtc-audio-recording.ts': { recordAudioReceiver: () => ({ close() {} }) }
  };
  const code = ts.transpileModule(
    fs.readFileSync(new URL('../src/hooks/useUsbAudio.ts', import.meta.url), 'utf8'),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }
  ).outputText;
  const window = {
    setTimeout: (fn, delay = 0) => schedule(fn, delay),
    clearTimeout: clear,
    setInterval: (fn, delay) => schedule(fn, delay, delay),
    clearInterval: clear,
    addEventListener() {},
    removeEventListener() {},
    location: { href: 'https://kvm.test/' }
  };
  const exports = {};
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      assert.ok(name in modules, name);
      return modules[name];
    },
    window,
    location: window.location,
    localStorage: {
      getItem: () => JSON.stringify({ enabled: true, volume: 80 }),
      setItem() {}
    },
    performance: { now: () => now },
    WebSocket: FakeSocket,
    RTCPeerConnection: FakePeer,
    MediaStream: class {},
    Audio: class {
      play() {
        return Promise.resolve();
      }
      pause() {}
    },
    URL,
    JSON,
    Promise,
    Object,
    Math,
    console
  });

  const flush = async () => {
    for (let round = 0; round < 20; round++) {
      await new Promise((resolve) => setImmediate(resolve));
      if (dirty || effects.some((e) => e?.pending)) {
        dirty = false;
        slot = 0;
        exports.useUsbAudio();
        for (const effect of effects) {
          if (!effect?.pending) continue;
          effect.pending = false;
          effect.cleanup?.();
          effect.cleanup = effect.fn() || undefined;
        }
      }
    }
  };
  const advance = async (ms) => {
    const end = now + ms;
    for (;;) {
      const due = [...timers.entries()]
        .filter(([, t]) => t.at <= end)
        .sort((a, b) => a[1].at - b[1].at)[0];
      if (!due) break;
      const [id, timer] = due;
      now = timer.at;
      if (timer.every) timer.at = now + timer.every;
      else timers.delete(id);
      timer.fn();
      await flush();
    }
    now = end;
    await flush();
  };
  const start = async () => {
    slot = 0;
    exports.useUsbAudio();
    for (const effect of effects) {
      if (!effect) continue;
      effect.pending = false;
      effect.cleanup = effect.fn() || undefined;
    }
    await flush();
  };
  // The server's side: ICE servers, then the connection; capture may fail.
  const answer = async (socket) => {
    socket.onmessage({ data: JSON.stringify({ event: 'ice-servers', data: [] }) });
    await flush();
  };
  return { sockets, peers, start, advance, flush, answer, now: () => now };
}

const realRandom = Math.random;

test('repeated capture failures after a connection back off', async () => {
  Math.random = () => 0.5;
  try {
    const h = harness();
    await h.start();
    const failedAt = [];
    for (let cycle = 0; cycle < 5; cycle++) {
      while (h.sockets.length <= cycle) await h.advance(100);
      const socket = h.sockets[cycle];
      await h.answer(socket);
      h.peers[cycle].connect();
      await h.flush();
      // Capture fails right after the connection, before any audio.
      socket.onmessage({ data: JSON.stringify({ event: 'error' }) });
      await h.flush();
      failedAt.push(h.now());
    }
    while (h.sockets.length <= 5) await h.advance(100);
    const waits = h.sockets.slice(1, 6).map((s, i) => s.created - failedAt[i]);
    // Rounded up to the 1 s retry tick: 2, 4, 8, 16, 32 s.
    assert.deepEqual(
      waits.map((ms) => Math.round(ms / 1000)),
      [2, 4, 8, 16, 32],
      `waits ${waits}`
    );
  } finally {
    Math.random = realRandom;
  }
});

test('audio that kept arriving starts the delay over', async () => {
  Math.random = () => 0.5;
  try {
    const h = harness();
    await h.start();
    // Two failures first: the next wait would be 8 s.
    for (let cycle = 0; cycle < 3; cycle++) {
      while (h.sockets.length <= cycle) await h.advance(100);
      await h.answer(h.sockets[cycle]);
      h.peers[cycle].connect();
      await h.flush();
      if (cycle < 2) {
        h.sockets[cycle].onmessage({ data: JSON.stringify({ event: 'error' }) });
        await h.flush();
      }
    }
    // Audio flows for a few seconds, then the connection drops.
    const peer = h.peers[2];
    for (let second = 0; second < 4; second++) {
      peer.packets += 50;
      await h.advance(1000);
    }
    h.sockets[2].onclose({ code: 1006 });
    await h.flush();
    const droppedAt = h.now();
    while (h.sockets.length <= 3) await h.advance(100);
    assert.equal(Math.round((h.sockets[3].created - droppedAt) / 1000), 2);
  } finally {
    Math.random = realRandom;
  }
});
