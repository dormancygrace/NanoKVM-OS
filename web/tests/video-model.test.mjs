import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const source = readFileSync(new URL('../src/lib/video-model.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 }
}).outputText;
const model = (() => {
  const sandbox = { exports: {}, module: {} };
  vm.runInNewContext(code, sandbox);
  return sandbox.exports;
})();

const tiers = [
  { longSide: 1280, shortSide: 720, fps: 120 },
  { longSide: 1920, shortSide: 1088, fps: 75 },
  { longSide: 2560, shortSide: 1440, fps: 50 },
  { longSide: 0, shortSide: 0, fps: 30 }
];

function caps({ uhd = true, input = [2560, 1440], follows = true, programmable = true } = {}) {
  const mode = (height, width, rates, available = true, reason) => ({
    height,
    width,
    rates,
    available,
    ...(reason ? { reason } : {})
  });
  return {
    input: { width: input[0], height: input[1], fps: 50, maxFps: 50 },
    monitor: {
      programmable,
      requiresPowerCycle: false,
      powerCyclePending: false,
      followsStreamRate: follows,
      selected: 0,
      refreshHz: 50,
      modes: [
        mode(0, 0, [50, 40, 30]),
        mode(2160, 3840, [30], uhd, uhd ? undefined : 'video-memory'),
        mode(1440, 2560, [50, 40, 30]),
        mode(1080, 1920, [75, 60, 30]),
        mode(720, 1280, [120, 60, 30])
      ],
      portrait: {
        enabled: false,
        resolution: 1920,
        profiles: [
          { resolution: 1920, width: 1080, rate: 75, codecs: ['mjpeg', 'h264', 'h265'], transports: ['direct', 'webrtc', 'mjpeg'], available: true },
          { resolution: 2560, width: 1440, rate: 50, codecs: ['h265'], transports: ['direct'], available: true }
        ]
      }
    },
    stream: {
      limits: [
        { height: 0, width: 0, available: true },
        { height: 2160, width: 3840, available: uhd, ...(uhd ? {} : { reason: 'video-memory' }) },
        { height: 1440, width: 2560, available: true },
        { height: 1080, width: 1920, available: true },
        { height: 720, width: 1280, available: true },
        { height: 600, width: 800, available: true }
      ],
      rateTiers: tiers,
      minFps: 10,
      maxFps: 120
    },
    transports: { direct: ['h264', 'h265'], webrtc: ['h264', 'h265'], mjpeg: ['mjpeg'] },
    videoMemoryMiB: uhd ? 128 : 64
  };
}

const everything = { direct: true, webrtc: true, directH265: true, webrtcH265: true };
const draft = (over = {}) => ({
  monitor: 0,
  portrait: 0,
  height: 0,
  fps: 50,
  transport: 'direct',
  codec: 'h265',
  bitRate: 3000,
  quality: 80,
  gop: 30,
  gopMode: 1,
  mjpegChroma: 422,
  directPlayback: 'paced',
  frameDetect: false,
  ...over
});

test('rate tiers follow the server table in either orientation', () => {
  assert.equal(model.rateLimit(tiers, 3840, 2160), 30);
  assert.equal(model.rateLimit(tiers, 1440, 2560), 50);
  assert.equal(model.rateLimit(tiers, 1088, 1920), 75);
  assert.equal(model.rateLimit(tiers, 1280, 720), 120);
});

test('effective rate is limited by the input size', () => {
  assert.equal(model.effectiveFps(draft({ fps: 60 }), caps({ input: [3840, 2160] })), 30);
  assert.equal(model.effectiveFps(draft({ fps: 60, height: 1080 }), caps({ input: [3840, 2160] })), 30);
  assert.equal(model.effectiveFps(draft({ fps: 120 }), caps({ input: [1280, 720] })), 120);
});

test('monitor refresh follows the stream rate', () => {
  const c = caps();
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 30 }), c).refresh, 30);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 50 }), c).refresh, 60);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 120 }), c).refresh, 75);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 30 }), caps({ follows: false })).refresh, 75);
});

test('H.265 over WebRTC is allowed when the browser decodes it', () => {
  const c = caps();
  assert.deepEqual({ ...model.draftIssues(draft({ transport: 'webrtc' }), c, everything) }, {});
  assert.equal(
    model.draftIssues(draft({ transport: 'webrtc' }), c, { ...everything, webrtcH265: false }).codec,
    'browser'
  );
});

test('the tallest portrait profile needs H.265 Direct', () => {
  const c = caps();
  assert.equal(model.draftIssues(draft({ portrait: 2560, transport: 'webrtc' }), c, everything).transport, 'portrait');
  assert.equal(model.draftIssues(draft({ portrait: 2560, codec: 'h264' }), c, everything).codec, 'portrait');
  assert.deepEqual({ ...model.draftIssues(draft({ portrait: 2560 }), c, everything) }, {});
});

test('unavailable choices carry their reason', () => {
  const c = caps({ uhd: false });
  assert.equal(model.draftIssues(draft({ monitor: 2160 }), c, everything).monitor, 'video-memory');
  assert.equal(model.draftIssues(draft({ height: 2160 }), c, everything).height, 'video-memory');
  assert.equal(model.draftIssues(draft({ fps: 5 }), c, everything).fps, 'range');
});

test('presets build coherent drafts', () => {
  const sharp = model.buildPreset('sharp', caps(), everything, draft()).draft;
  assert.equal(sharp.monitor, 2160);
  assert.equal(sharp.fps, 30);
  assert.equal(sharp.codec, 'h265');
  const sharpQhd = model.buildPreset('sharp', caps({ uhd: false }), everything, draft()).draft;
  assert.equal(sharpQhd.monitor, 1440);
  assert.equal(sharpQhd.fps, 50);
  const responsive = model.buildPreset('responsive', caps(), everything, draft()).draft;
  assert.deepEqual([responsive.monitor, responsive.fps, responsive.directPlayback], [720, 120, 'immediate']);
  const compatible = model.buildPreset('compatible', caps(), everything, draft()).draft;
  assert.deepEqual([compatible.transport, compatible.codec, compatible.fps], ['webrtc', 'h264', 60]);
  const saver = model.buildPreset('saver', caps(), everything, draft()).draft;
  assert.equal(model.monitorTarget(saver, caps()).refresh, 30);
  assert.equal(saver.height, 1080);
});

test('presets fall back to what the browser plays', () => {
  const noDirect = { direct: false, webrtc: true, directH265: false, webrtcH265: false };
  const auto = model.buildPreset('auto', caps(), noDirect, draft()).draft;
  assert.deepEqual([auto.transport, auto.codec], ['webrtc', 'h264']);
  const nothing = { direct: false, webrtc: false, directH265: false, webrtcH265: false };
  assert.equal(model.buildPreset('sharp', caps(), nothing, draft()).reason, 'browser');
  assert.equal(model.buildPreset('compatible', caps(), nothing, draft()).draft.transport, 'mjpeg');
});

test('presets keep a portrait monitor and a fixed receiver', () => {
  const portrait = model.buildPreset('smooth', caps(), everything, draft({ portrait: 1920 })).draft;
  assert.equal(portrait.portrait, 1920);
  assert.equal(portrait.monitor, 0);
  const fixed = model.buildPreset('sharp', caps({ programmable: false }), everything, draft()).draft;
  assert.equal(fixed.monitor, 0);
});

test('a preset is recognized after it is applied', () => {
  for (const id of model.PRESETS) {
    const built = model.buildPreset(id, caps(), everything, draft());
    assert.equal(model.matchPreset(built.draft, caps(), everything), id);
  }
  assert.equal(model.matchPreset(draft({ bitRate: 15000, gop: 7 }), caps(), everything), 'custom');
});

test('a slower stream rate rewrites the monitor only when it follows the rate', () => {
  const saved = draft({ monitor: 1080, fps: 60 });
  assert.equal(model.draftChanges(saved, { ...saved, fps: 30 }, caps()).monitorRewrite, true);
  assert.equal(model.draftChanges(saved, { ...saved, fps: 50 }, caps()).monitorRewrite, false);
  assert.equal(model.draftChanges(saved, { ...saved, fps: 30 }, caps({ follows: false })).monitorRewrite, false);
  assert.equal(model.draftChanges(saved, { ...saved, codec: 'h264' }, caps()).reconnect, true);
});

test('the request carries changed shared settings only', () => {
  const saved = draft();
  assert.deepEqual({ ...model.settingsRequest(saved, { ...saved, fps: 30, directPlayback: 'immediate' }) }, { fps: 30 });
  assert.deepEqual({ ...model.settingsRequest(saved, { ...saved, codec: 'h264' }) }, { type: 1 });
  assert.deepEqual(
    { ...model.settingsRequest(saved, { ...saved, portrait: 2560 }) },
    { portraitResolution: 2560, portrait: true }
  );
  assert.deepEqual(
    { ...model.settingsRequest({ ...saved, portrait: 1920 }, { ...saved, portrait: 2560 }) },
    { portraitResolution: 2560 }
  );
  assert.deepEqual({ ...model.settingsRequest({ ...saved, portrait: 1920 }, saved) }, { portrait: false });
});

test('the measured input rate is shown as its nominal rate', () => {
  assert.equal(model.nominalRate(51), 50);
  assert.equal(model.nominalRate(29), 30);
  assert.equal(model.nominalRate(59), 60);
  assert.equal(model.nominalRate(44), 44);
});
