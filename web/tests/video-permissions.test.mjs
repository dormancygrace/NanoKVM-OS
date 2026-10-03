import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const status = {
  monitor: 0, portrait: false, portraitResolution: 1920, height: 1080,
  fps: 60, quality: 80, bitRate: 3000, gop: 30, gopMode: 1, mjpegChroma: 420,
  gopModeActive: 1, gopModeRestartRequired: false, monitorSupported: true,
  monitorRequiresPowerCycle: false, monitorPowerCyclePending: false,
  monitorHighRefreshSupported: true, qhdSupported: true, portraitSupported: true,
  portraitMaxSupported: true, inputWidth: 1920, inputHeight: 1080
};

function render(file, { role = 'user', draft = {} } = {}) {
  const calls = { shared: [], encoder: [], local: [], errors: [], reloads: 0, refreshes: 0 };
  let stateIndex = 0;
  const element = (type, props) => ({ type, props: props ?? {} });
  const atoms = Object.fromEntries(
    ['resolutionAtom', 'streamFpsAtom', 'streamGopAtom', 'streamQualityAtom', 'videoModeAtom']
      .map((name) => [name, name])
  );
  const values = {
    resolutionAtom: { width: 1920, height: 1080 }, streamFpsAtom: 60,
    streamGopAtom: 30, streamQualityAtom: 2, videoModeAtom: 'direct'
  };
  const storage = {
    getFrameDetect: () => false,
    getDirectPlayback: () => 'paced',
    setVideoMode: (value) => calls.local.push(['mode', value]),
    setDirectPlayback: (value) => calls.local.push(['playback', value]),
    setFrameDetect: () => assert.fail('viewer must not persist frame detection'),
    setResolution: () => {}, setFps: () => {}, setGop: () => {}, setQuality: () => {}
  };
  const components = Object.fromEntries(
    ['Alert', 'Button', 'Checkbox', 'Collapse', 'InputNumber', 'Modal', 'Select', 'Switch']
      .map((name) => [name, name])
  );
  const modules = {
    react: {
      useEffect: () => {},
      useRef: (value) => ({ current: value }),
      useState: (initial) => {
        const value = typeof initial === 'function' ? initial() : initial;
        return [stateIndex++ === 0 ? { ...value, ...draft } : value, () => {}];
      }
    },
    'react/jsx-runtime': { jsx: element, jsxs: element, Fragment: 'Fragment' },
    '@/contexts/auth': { useAuth: () => ({ account: { role } }) },
    'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
    jotai: {
      useAtomValue: (atom) => values[atom],
      useAtom: (atom) => [values[atom], () => {}],
      useSetAtom: () => () => {}
    },
    antd: {
      ...components,
      message: { success: () => {}, error: (error) => calls.errors.push(error) }
    },
    '@/api/stream': {
      selectEncoderCodec: async (codec) => {
        calls.encoder.push(codec);
        return { code: 0 };
      },
      updateFrameDetect: async (value) => {
        calls.shared.push(['frameDetect', value]);
        return { code: 0 };
      }
    },
    '@/api/vm': {
      getScreen: async () => ({ code: 0, data: status }),
      updateScreen: async (...args) => {
        calls.shared.push(args);
        return { code: 0 };
      }
    },
    '@/lib/encoder': {
      getEncoderCodec: () => 'h264',
      isEncoderCodecSupported: async () => true,
      setEncoderCodec: (value) => calls.local.push(['codec', value])
    },
    '@/lib/localstorage': storage,
    '@/lib/video-policy': { isQhdStream: () => false },
    '@/jotai/screen': atoms,
    '../../screen/constants': { getQualityMap: () => new Map([[2, 3000]]) },
    './constants': { getQualityMap: () => new Map([[2, 3000]]) },
    '../../screen/reset': { Reset: 'Reset' },
    './fps': { Fps: 'Fps' }, './gop': { Gop: 'Gop' },
    './quality': { Quality: 'Quality' }, './resolution': { Resolution: 'Resolution' }
  };
  const exports = {};
  const code = ts.transpileModule(readFileSync(new URL(file, import.meta.url), 'utf8'), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022,
      jsx: ts.JsxEmit.ReactJSX
    }
  }).outputText;
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      if (!(name in modules)) throw new Error(`unmocked import ${name}`);
      return modules[name];
    },
    URL,
    window: {
      isSecureContext: true, VideoDecoder: {}, RTCPeerConnection: {},
      location: { href: 'https://kvm.test/', reload: () => { calls.reloads++; } },
      history: { state: null, replaceState: () => {} }
    }
  });
  const tree = file.includes('form.tsx')
    ? exports.VideoForm({
        status, refresh: async () => { calls.refreshes++; }, setIsLocked: () => {}
      })
    : exports.StreamControls({ advanced: false });
  return { tree, calls };
}

function nodes(tree) {
  if (!tree || typeof tree !== 'object') return [];
  return [tree, ...Object.values(tree).flatMap(nodes)];
}

const form = '../src/pages/desktop/menu/settings/video/form.tsx';
const controls = '../src/pages/desktop/menu/screen/controls.tsx';

test('users see shared values without mounting mutable toolbar controls', () => {
  const { tree } = render(controls);
  const types = nodes(tree).map((node) => node.type);
  for (const type of ['Fps', 'Quality', 'Gop', 'Resolution']) {
    assert.equal(types.includes(type), false, `${type} must be read-only for users`);
  }
  assert.ok(nodes(tree).some((node) => node.props?.children === 'screen.fps'));
});

test('administrators retain mutable toolbar controls', () => {
  const { tree } = render(controls, { role: 'admin' });
  const types = nodes(tree).map((node) => node.type);
  for (const type of ['Fps', 'Quality', 'Resolution']) assert.ok(types.includes(type));
});

test('users can choose transport and playback while shared codec is read-only', () => {
  const { tree } = render(form);
  const select = (label) => nodes(tree).find((node) => node.type === 'Select' && node.props['aria-label'] === label);
  assert.equal(select('screen.codec').props.disabled, true);
  assert.equal(select('screen.video').props.disabled, false);
  assert.equal(select('videoSettings.directPlayback').props.disabled, false);
});

for (const [name, draft, expected] of [
  ['WebRTC transport', { mode: 'h264' }, ['mode', 'h264']],
  ['MJPEG transport', { mode: 'mjpeg' }, ['mode', 'mjpeg']],
  ['Direct playback', { directPlayback: 'immediate' }, ['playback', 'immediate']],
  ['transport after role demotion with a pending shared draft', { mode: 'h264', fps: 75, gop: 10 }, ['mode', 'h264']]
]) {
  test(`user applies ${name} without calling shared-setting APIs`, async () => {
    const { tree, calls } = render(form, { draft });
    const apply = nodes(tree).find((node) => node.type === 'Button' && node.props.children === 'videoSettings.apply');
    assert.equal(apply.props.disabled, false);
    await apply.props.onClick();
    // The click callback starts an async action; wait for its refresh/finally.
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(calls.shared, []);
    assert.deepEqual(calls.encoder, []);
    assert.deepEqual(calls.errors, []);
    assert.ok(calls.local.some((entry) => entry[0] === expected[0] && entry[1] === expected[1]));
    assert.equal(calls.reloads, 1);
    assert.equal(calls.refreshes, 1);
  });
}

test('administrator transport changes still select the shared encoder', async () => {
  const { tree, calls } = render(form, { role: 'admin', draft: { mode: 'h264' } });
  const apply = nodes(tree).find((node) => node.type === 'Button' && node.props.children === 'videoSettings.apply');
  await apply.props.onClick();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(calls.encoder, ['h264']);
  assert.deepEqual(calls.errors, []);
  assert.equal(calls.reloads, 1);
});

test('administrator can still apply a shared FPS preference', async () => {
  const { tree, calls } = render(form, { role: 'admin', draft: { fps: 75 } });
  const apply = nodes(tree).find((node) => node.type === 'Button' && node.props.children === 'videoSettings.apply');
  await apply.props.onClick();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(calls.shared, [['fps', 75, false]]);
  assert.deepEqual(calls.encoder, []);
  assert.deepEqual(calls.errors, []);
  assert.equal(calls.reloads, 0);
  assert.equal(calls.refreshes, 1);
});
