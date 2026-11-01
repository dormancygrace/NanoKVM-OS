import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

function transpile(file, jsx = false) {
  return ts.transpileModule(readFileSync(new URL(file, import.meta.url), 'utf8'), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
      ...(jsx ? { jsx: ts.JsxEmit.ReactJSX } : {})
    }
  }).outputText;
}

function load(file, modules, globals = {}, jsx = false) {
  const exports = {};
  vm.runInNewContext(transpile(file, jsx), {
    exports,
    require: (name) => {
      if (!(name in modules)) throw new Error(`unmocked import ${name}`);
      return modules[name];
    },
    URL,
    ...globals
  });
  return exports;
}

const model = load('../src/lib/video-model.ts', {});

const caps = {
  input: { width: 1920, height: 1080, fps: 60, maxFps: 75 },
  monitor: {
    programmable: true,
    requiresPowerCycle: false,
    powerCyclePending: false,
    followsStreamRate: true,
    selected: 1080,
    refreshHz: 60,
    modes: [
      { height: 0, width: 0, rates: [50, 40, 30], available: true },
      { height: 1080, width: 1920, rates: [75, 60, 30], available: true }
    ],
    portrait: { enabled: false, resolution: 1920, profiles: [] }
  },
  stream: {
    limits: [
      { height: 0, width: 0, available: true },
      { height: 1080, width: 1920, available: true }
    ],
    rateTiers: [
      { longSide: 1920, shortSide: 1088, fps: 75 },
      { longSide: 0, shortSide: 0, fps: 30 }
    ],
    minFps: 10,
    maxFps: 120
  },
  transports: { direct: ['h264', 'h265'], webrtc: ['h264', 'h265'], mjpeg: ['mjpeg'] },
  videoMemoryMiB: 56
};
const browser = { direct: true, webrtc: true, directH265: true, webrtcH265: true };
const saved = {
  monitor: 1080,
  portrait: 0,
  height: 0,
  fps: 60,
  transport: 'direct',
  codec: 'h264',
  bitRate: 3000,
  quality: 80,
  gop: 30,
  gopMode: 1,
  mjpegChroma: 420,
  directPlayback: 'paced',
  frameDetect: false
};

function applier() {
  const calls = { shared: [], encoder: [], local: [], reloads: 0 };
  const apply = load('../src/pages/desktop/menu/settings/video/apply.ts', {
    '@/api/stream': {},
    '@/api/vm': {},
    '@/lib/encoder': {},
    '@/lib/localstorage': {},
    '@/lib/video-model': model
  });
  const deps = {
    applyVideoSettings: async (body, confirm) => {
      calls.shared.push([{ ...body }, confirm]);
      return { code: 0, data: null };
    },
    selectEncoderCodec: async (codec) => {
      calls.encoder.push(codec);
      return { code: 0 };
    },
    updateFrameDetect: async (value) => {
      calls.shared.push(['frameDetect', value]);
      return { code: 0 };
    },
    setEncoderCodec: (codec) => calls.local.push(['codec', codec]),
    storage: {
      setVideoMode: (value) => calls.local.push(['mode', value]),
      setDirectPlayback: (value) => calls.local.push(['playback', value])
    },
    reload: () => calls.reloads++,
    clearPlaybackOverrides: () => {}
  };
  return { apply: apply.applyVideoDraft, deps, calls };
}

for (const [name, change, expected] of [
  ['WebRTC transport', { transport: 'webrtc' }, ['mode', 'h264']],
  ['MJPEG transport', { transport: 'mjpeg' }, ['mode', 'mjpeg']],
  ['Direct playback', { directPlayback: 'immediate' }, ['playback', 'immediate']],
  [
    'transport with a pending shared draft after role demotion',
    { transport: 'webrtc', fps: 75, gop: 10 },
    ['mode', 'h264']
  ]
]) {
  test(`user applies ${name} without calling shared-setting APIs`, async () => {
    const { apply, deps, calls } = applier();
    const result = await apply(
      saved,
      { ...saved, ...change },
      caps,
      { admin: false, confirmPowerCycle: false },
      deps
    );
    assert.deepEqual(calls.shared, []);
    assert.deepEqual(calls.encoder, []);
    assert.ok(calls.local.some((e) => e[0] === expected[0] && e[1] === expected[1]));
    assert.equal(calls.reloads, 1);
    assert.equal(result.reloading, true);
  });
}

test('administrator transport changes select the shared encoder', async () => {
  const { apply, deps, calls } = applier();
  await apply(
    saved,
    { ...saved, transport: 'webrtc' },
    caps,
    { admin: true, confirmPowerCycle: false },
    deps
  );
  assert.deepEqual(calls.encoder, ['h264']);
  // An administrator's transport is the device transport too.
  assert.deepEqual(calls.shared, [[{ transport: 'webrtc' }, false]]);
  assert.equal(calls.reloads, 1);
});

test('administrator applies shared settings in one request', async () => {
  const { apply, deps, calls } = applier();
  const result = await apply(
    saved,
    { ...saved, fps: 30, bitRate: 5000 },
    caps,
    { admin: true, confirmPowerCycle: false },
    deps
  );
  assert.deepEqual(calls.shared, [[{ bitRate: 5000, fps: 30 }, false]]);
  assert.deepEqual(calls.encoder, []);
  assert.equal(calls.reloads, 0);
  // 30 fps selects the 1080p 30 Hz monitor.
  assert.equal(result.monitorRewritten, true);
});

test('a rejected request leaves this browser unchanged', async () => {
  const { apply, deps, calls } = applier();
  deps.applyVideoSettings = async () => ({ code: -3, msg: 'needs video memory' });
  await assert.rejects(
    apply(
      saved,
      { ...saved, transport: 'webrtc', fps: 30 },
      caps,
      { admin: true, confirmPowerCycle: false },
      deps
    ),
    /needs video memory/
  );
  assert.deepEqual(calls.local, []);
  assert.equal(calls.reloads, 0);
});

test('a resolution change applies in one request, before the encoder, and returns the capabilities', async () => {
  const { apply, deps, calls } = applier();
  const capabilities = { videoMemoryMiB: 144 };
  deps.applyVideoSettings = async (...args) => {
    calls.shared.push([{ ...args[0] }, ...args.slice(1)]);
    return { code: 0, data: capabilities };
  };
  const result = await apply(
    saved,
    { ...saved, monitor: 2160, transport: 'webrtc', codec: 'h265', frameDetect: true },
    caps,
    { admin: true, confirmPowerCycle: false },
    deps
  );
  // No video memory flag: every resolution fits the shared video memory.
  assert.deepEqual(calls.shared, [
    [{ transport: 'webrtc', type: 2, monitor: 2160 }, false],
    ['frameDetect', true]
  ]);
  assert.deepEqual(calls.encoder, ['h265']);
  assert.equal(result.capabilities, capabilities);
  assert.equal(result.monitorRewritten, true);
  assert.equal(calls.reloads, 1);
  assert.equal(result.reloading, true);
});
