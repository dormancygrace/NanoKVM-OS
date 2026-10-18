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
    ['Alert', 'Button', 'Collapse', 'InputNumber', 'Modal', 'Select', 'Switch'].map((n) => [n, n])
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
      '@/api/vm': { getMemoryStatus: async () => ({}), reboot: async () => ({}), setVideoMemory: async () => ({}) },
      '@/lib/video-memory-mode': { pickVideoMemoryMode: () => undefined },
      '@/lib/video-model': model,
      '../../screen/reset': { Reset: 'Reset' },
      './apply': { applyVideoDraft: async () => ({}) },
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
  const control = (type, label) =>
    nodes.find((n) => n.type === type && n.props['aria-label'] === label);
  assert.equal(control('Select', 'videoSettings.resolution').props.disabled, true);
  assert.equal(control('Select', 'videoSettings.frameRate').props.disabled, true);
  assert.equal(control('Select', 'videoSettings.bitrate').props.disabled, true);
  assert.equal(control('Select', 'screen.codec').props.disabled, true);
  assert.equal(control('InputNumber', 'videoSettings.fpsOverride').props.disabled, true);
  assert.equal(control('Select', 'screen.video').props.disabled, false);
  assert.equal(control('Select', 'videoSettings.player').props.disabled, false);
});

test('administrators edit the shared picture', () => {
  const nodes = renderForm('admin');
  const control = (type, label) =>
    nodes.find((n) => n.type === type && n.props['aria-label'] === label);
  assert.equal(control('Select', 'videoSettings.resolution').props.disabled, false);
  assert.equal(control('Select', 'videoSettings.frameRate').props.disabled, false);
  assert.equal(control('Select', 'videoSettings.bitrate').props.disabled, false);
  assert.equal(control('Select', 'screen.codec').props.disabled, false);
  assert.equal(control('InputNumber', 'videoSettings.fpsOverride').props.disabled, false);
});

function renderQuickVideo(role, session) {
  const applied = [];
  const element = (type, props) => ({ type, props: props ?? {} });
  const quick = load(
    '../src/pages/desktop/menu/screen/quick-video.tsx',
    {
      react: { useState: (initial) => [initial, () => {}] },
      'react/jsx-runtime': { jsx: element, jsxs: element, Fragment: 'Fragment' },
      'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
      antd: { message: { success: () => {}, error: () => {}, info: () => {} } },
      'lucide-react': { CheckIcon: 'CheckIcon', GaugeIcon: 'GaugeIcon', MonitorIcon: 'MonitorIcon', RadioTowerIcon: 'RadioTowerIcon' },
      '@/lib/video-model': model,
      '@/components/menu-item.tsx': { MenuSubmenu: 'MenuSubmenu' },
      '../settings/video/apply': {
        applyVideoDraft: async (before, draft, _caps, options) => {
          applied.push({ draft, admin: options.admin });
          return { reloading: true };
        }
      },
      '../settings/video/use-video-settings': {
        useSyncStreamAtoms: () => () => {},
        useVideoSettings: () => ({
          admin: role === 'admin',
          caps,
          browser: session.browser,
          saved: session.saved,
          status: { measuredFps: 60, measuredKbps: 3000, outputWidth: 1920, outputHeight: 1080 },
          refresh: async () => {}
        })
      }
    },
    {},
    true
  );
  const tree = quick.QuickVideo();
  const nodes = (n) => (!n || typeof n !== 'object' ? [] : [n, ...Object.values(n).flatMap(nodes)]);
  const row = nodes(tree).find(
    (n) => typeof n.type === 'function' && n.props.title === 'videoSettings.transport'
  );
  return { items: row.props.items, applied };
}

test('a user is not offered a transport that needs another shared codec', async () => {
  // Direct/H.265 in a browser without WebRTC H.265.
  const session = {
    browser: { ...browser, webrtcH265: false },
    saved: { ...saved, transport: 'direct', codec: 'h265' }
  };
  const user = renderQuickVideo('user', session);
  const webrtc = user.items.find((item) => item.key === 'webrtc');
  assert.equal(webrtc.disabled, true);
  assert.equal(webrtc.label, 'WebRTC');
  webrtc.pick();
  await Promise.resolve();
  assert.deepEqual(user.applied, []);

  // An administrator gets WebRTC with H.264, which changes the shared codec.
  const admin = renderQuickVideo('admin', session);
  const fallback = admin.items.find((item) => item.key === 'webrtc');
  assert.equal(fallback.disabled, false);
  assert.equal(fallback.label, 'WebRTC · H.264');
  fallback.pick();
  await Promise.resolve();
  assert.equal(admin.applied.length, 1);
  assert.equal(admin.applied[0].draft.transport, 'webrtc');
  assert.equal(admin.applied[0].draft.codec, 'h264');
});
