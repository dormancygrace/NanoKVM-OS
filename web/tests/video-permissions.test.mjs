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
  videoMemoryMiB: 64
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
    const result = await apply(saved, { ...saved, ...change }, caps, { admin: false, confirmPowerCycle: false }, deps);
    assert.deepEqual(calls.shared, []);
    assert.deepEqual(calls.encoder, []);
    assert.ok(calls.local.some((e) => e[0] === expected[0] && e[1] === expected[1]));
    assert.equal(calls.reloads, 1);
    assert.equal(result.reloading, true);
  });
}

test('administrator transport changes select the shared encoder', async () => {
  const { apply, deps, calls } = applier();
  await apply(saved, { ...saved, transport: 'webrtc' }, caps, { admin: true, confirmPowerCycle: false }, deps);
  assert.deepEqual(calls.encoder, ['h264']);
  assert.equal(calls.reloads, 1);
});

test('administrator applies shared settings in one request', async () => {
  const { apply, deps, calls } = applier();
  const result = await apply(saved, { ...saved, fps: 30, bitRate: 5000 }, caps, { admin: true, confirmPowerCycle: false }, deps);
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
    apply(saved, { ...saved, transport: 'webrtc', fps: 30 }, caps, { admin: true, confirmPowerCycle: false }, deps),
    /needs video memory/
  );
  assert.deepEqual(calls.local, []);
  assert.equal(calls.reloads, 0);
});

function renderForm(role) {
  let stateIndex = 0;
  const element = (type, props) => ({ type, props: props ?? {} });
  const components = Object.fromEntries(
    ['Button', 'Collapse', 'InputNumber', 'Modal', 'Select', 'Switch'].map((n) => [n, n])
  );
  const form = load(
    '../src/pages/desktop/menu/settings/video/form.tsx',
    {
      react: {
        useEffect: () => {},
        useRef: (value) => ({ current: value }),
        useState: (initial) => [typeof initial === 'function' ? initial() : initial, () => {}]
      },
      'react/jsx-runtime': { jsx: element, jsxs: element, Fragment: 'Fragment' },
      'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
      antd: { ...components, message: { success: () => {}, error: () => {} } },
      '@/lib/video-model': model,
      '../../screen/reset': { Reset: 'Reset' },
      './apply': { applyVideoDraft: async () => ({}) },
      './presets': { PresetPicker: 'PresetPicker' },
      './use-video-settings': { useSyncStreamAtoms: () => () => {} }
    },
    {},
    true
  );
  void stateIndex;
  const tree = form.VideoForm({
    caps,
    browser,
    saved,
    admin: role === 'admin',
    refresh: async () => {},
    setIsLocked: () => {}
  });
  const nodes = (n) => (!n || typeof n !== 'object' ? [] : [n, ...Object.values(n).flatMap(nodes)]);
  return nodes(tree);
}

test('users choose transport and playback; shared settings are read-only', () => {
  const nodes = renderForm('user');
  const select = (label) =>
    nodes.find((n) => n.type === 'Select' && n.props['aria-label'] === label);
  assert.equal(select('screen.codec').props.disabled, true);
  assert.equal(select('screen.fps').props.disabled, true);
  assert.equal(select('videoSettings.monitorProfile').props.disabled, true);
  assert.equal(select('screen.video').props.disabled, false);
  assert.equal(select('videoSettings.directPlayback').props.disabled, false);
  assert.equal(nodes.some((n) => n.type === 'PresetPicker'), false);
});

test('administrators see presets and editable shared settings', () => {
  const nodes = renderForm('admin');
  const select = (label) =>
    nodes.find((n) => n.type === 'Select' && n.props['aria-label'] === label);
  assert.equal(select('screen.codec').props.disabled, false);
  assert.equal(select('screen.fps').props.disabled, false);
  assert.ok(nodes.some((n) => n.type === 'PresetPicker'));
});
