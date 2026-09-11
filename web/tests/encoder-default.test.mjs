import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const source = ts.transpileModule(readFileSync(new URL('../src/lib/encoder.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 }
}).outputText;
function browser({ stored, supported = true, decoder = true, secure = true, reject = false, hang = false, rtc = false } = {}) {
  const writes = [], probes = [];
  const exports = {};
  const context = vm.createContext({ exports, Promise,
    setTimeout: hang ? (cb) => { queueMicrotask(cb); return 0; } : setTimeout,
    clearTimeout: hang ? () => {} : clearTimeout,
    localStorage: { getItem: () => stored ?? null, setItem: (k, v) => { stored = v; writes.push(v); } },
    window: { isSecureContext: secure,
      VideoDecoder: decoder ? { isConfigSupported: (config) => {
        probes.push(config);
        return hang ? new Promise(() => {}) : reject ? Promise.reject(new Error('unsupported')) : Promise.resolve({ supported });
      }} : undefined,
      RTCRtpReceiver: { getCapabilities: () => ({ codecs: rtc ? [{ mimeType: 'video/H265' }] : [] }) }
    }
  });
  vm.runInContext(source, context);
  return { api: exports, writes, probes };
}

test('fresh browser chooses HEVC only after matching WebCodecs probe', async () => {
  const { api, probes, writes } = browser();
  assert.equal(api.getEncoderCodec(), 'h264');
  assert.equal(await api.initializeEncoderCodec('direct'), 'h265');
  assert.equal(api.getEncoderCodec(), 'h265');
  assert.equal(probes[0].codec, api.DIRECT_H265_CODEC);
  assert.equal(probes[0].hardwareAcceleration, 'prefer-hardware');
  assert.deepEqual(writes, []);
});
for (const options of [{ supported: false }, { decoder: false }, { secure: false }, { reject: true }, { hang: true }]) {
  test(`unsupported or unavailable HEVC falls back: ${JSON.stringify(options)}`, async () => {
    const { api, writes } = browser(options);
    assert.equal(await api.initializeEncoderCodec('direct'), 'h264');
    assert.equal(api.getEncoderCodec(), 'h264');
    assert.deepEqual(writes, []);
  });
}
test('explicit AVC preference is honored on an HEVC-capable browser', async () => {
  const { api, probes } = browser({ stored: 'h264' });
  assert.equal(await api.initializeEncoderCodec('direct'), 'h264');
  assert.equal(probes.length, 0);
});
test('unsupported saved HEVC falls back without overwriting manual preference', async () => {
  const { api, writes } = browser({ stored: 'h265', supported: false });
  assert.equal(await api.initializeEncoderCodec('direct'), 'h264');
  assert.deepEqual(writes, []);
});
test('player and menu reuse the same capability result', async () => {
  const { api, probes } = browser();
  await api.initializeEncoderCodec('direct');
  assert.equal(await api.isEncoderCodecSupported('direct', 'h265'), true);
  assert.equal(probes.length, 1);
});
test('manual changes update the effective codec and persist', async () => {
  const { api, writes } = browser();
  await api.initializeEncoderCodec('direct');
  api.setEncoderCodec('h264');
  assert.equal(api.getEncoderCodec(), 'h264');
  assert.deepEqual(writes, ['h264']);
});
test('WebRTC capability is independent of Direct HEVC support', async () => {
  const { api, probes } = browser({ supported: true, rtc: false });
  assert.equal(await api.initializeEncoderCodec('webrtc'), 'h264');
  assert.equal(probes.length, 0);
});

for (const [active, stored] of [['h264', 'h265'], ['h265', 'h264']]) {
  test(`joining viewer adopts active ${active}, preserving saved ${stored}`, async () => {
    const { api, writes } = browser({ stored });
    assert.equal(await api.initializeEncoderCodec('direct', active), active);
    assert.equal(api.getEncoderCodec(), active);
    assert.deepEqual(writes, []);
    assert.equal(await api.initializeEncoderCodec('direct'), stored);
  });
}
test('unsupported active HEVC is reported without requesting a conflicting AVC stream', async () => {
  const { api, writes } = browser({ supported: false, stored: 'h264' });
  await assert.rejects(api.initializeEncoderCodec('direct', 'h265'), /active-codec-unsupported/);
  assert.equal(api.getEncoderCodec(), 'h265');
  assert.deepEqual(writes, []);
});
test('joining WebRTC uses its own codec capability', async () => {
  const { api } = browser({ supported: true, rtc: false });
  await assert.rejects(api.initializeEncoderCodec('webrtc', 'h265'), /active-codec-unsupported/);
  assert.equal(await api.initializeEncoderCodec('direct', 'h265'), 'h265');
});
