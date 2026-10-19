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

// The server table (server/common/video_status.go).
const tiers = [
  { longSide: 1280, shortSide: 720, fps: 120 },
  { longSide: 1920, shortSide: 1088, fps: 100 },
  { longSide: 2560, shortSide: 1440, fps: 60 },
  { longSide: 0, shortSide: 0, fps: 30 }
];

function caps({
  uhd = true,
  input = [2560, 1440],
  follows = true,
  programmable = true,
  mjpegMaxSide = 2560
} = {}) {
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
        { ...mode(0, 0, [100, 75, 60, 30]), effectiveWidth: 1920, effectiveHeight: 1080 },
        mode(2160, 3840, [30], uhd, uhd ? undefined : 'video-memory'),
        mode(1440, 2560, [60, 50, 40, 30]),
        mode(1080, 1920, [100, 75, 60, 30]),
        mode(720, 1280, [120, 60, 30])
      ],
      portrait: {
        enabled: false,
        resolution: 1920,
        profiles: [
          {
            resolution: 1920,
            width: 1080,
            rate: 100,
            rates: [100, 75, 60, 30],
            codecs: ['mjpeg', 'h264', 'h265'],
            transports: ['direct', 'webrtc', 'mjpeg'],
            available: true
          },
          {
            resolution: 2560,
            width: 1440,
            rate: 60,
            rates: [60, 50, 30],
            codecs: ['h265'],
            transports: ['direct', 'webrtc'],
            available: true
          }
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
      maxFps: 120,
      ...(mjpegMaxSide ? { mjpeg: { available: true, maxSide: mjpegMaxSide } } : {})
    },
    transports: { direct: ['h264', 'h265'], webrtc: ['h264', 'h265'], mjpeg: ['mjpeg'] },
    videoMemoryMiB: uhd ? 144 : 56
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
  mjpegChroma: 422,
  directPlayback: 'paced',
  frameDetect: false,
  ...over
});

test('rate tiers follow the server table in either orientation', () => {
  assert.equal(model.rateLimit(tiers, 3840, 2160), 30);
  assert.equal(model.rateLimit(tiers, 1440, 2560), 60);
  assert.equal(model.rateLimit(tiers, 1088, 1920), 100);
  assert.equal(model.rateLimit(tiers, 1280, 720), 120);
});

test('effective rate is limited by the input size', () => {
  assert.equal(model.effectiveFps(draft({ fps: 60 }), caps({ input: [3840, 2160] })), 30);
  assert.equal(
    model.effectiveFps(draft({ fps: 60, height: 1080 }), caps({ input: [3840, 2160] })),
    30
  );
  assert.equal(model.effectiveFps(draft({ fps: 120 }), caps({ input: [1280, 720] })), 120);
});

test('monitor refresh follows the stream rate', () => {
  const c = caps();
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 30 }), c).refresh, 30);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 50 }), c).refresh, 60);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 120 }), c).refresh, 100);
  assert.equal(
    model.monitorTarget(draft({ monitor: 1080, fps: 30 }), caps({ follows: false })).refresh,
    100
  );
});

test('H.265 over WebRTC is allowed when the browser decodes it', () => {
  const c = caps();
  assert.deepEqual({ ...model.draftIssues(draft({ transport: 'webrtc' }), c, everything) }, {});
  assert.equal(
    model.draftIssues(draft({ transport: 'webrtc' }), c, { ...everything, webrtcH265: false })
      .codec,
    'browser'
  );
});

test('the tallest portrait profile needs H.265, over Direct or WebRTC', () => {
  const c = caps();
  assert.equal(
    model.draftIssues(draft({ portrait: 2560, transport: 'mjpeg' }), c, everything).transport,
    'portrait'
  );
  assert.equal(
    model.draftIssues(draft({ portrait: 2560, codec: 'h264' }), c, everything).codec,
    'portrait'
  );
  assert.deepEqual(
    { ...model.draftIssues(draft({ portrait: 2560, transport: 'webrtc' }), c, everything) },
    {}
  );
  assert.deepEqual({ ...model.draftIssues(draft({ portrait: 2560 }), c, everything) }, {});
});

test('unavailable choices carry their reason', () => {
  const c = caps({ uhd: false });
  assert.equal(model.draftIssues(draft({ monitor: 2160 }), c, everything).monitor, 'video-memory');
  assert.equal(model.draftIssues(draft({ height: 2160 }), c, everything).height, 'video-memory');
  assert.equal(model.draftIssues(draft({ fps: 5 }), c, everything).fps, 'range');
});

test('resolutions list every monitor size with its rates, then portrait', () => {
  const choices = model.resolutionChoices(caps());
  assert.deepEqual(
    [...choices.map((c) => c.key)],
    ['m2160', 'm1440', 'm1080', 'm720', 'p1920', 'p2560']
  );
  assert.deepEqual([...choices.find((c) => c.key === 'm1080').rates], [100, 75, 60, 30]);
  assert.ok(!choices.some((c) => c.key === 'm0'), 'Auto is not a resolution of its own');
  const uhd = model.resolutionChoices(caps({ uhd: false })).find((c) => c.key === 'm2160');
  assert.deepEqual([uhd.available, uhd.reason], [false, 'video-memory']);
});

test('a fixed receiver offers the input size at the stream rates', () => {
  const fixed = caps({ programmable: false });
  const choices = model.resolutionChoices(fixed);
  assert.deepEqual([...choices.map((c) => c.key)], ['i1440']);
  assert.deepEqual([...choices[0].rates], [60, 50, 40, 30]);
  assert.equal(model.resolutionOf(draft({ fps: 50 }), fixed).key, 'i1440');
});

test('the recommended resolution is 1440p, else 1080p', () => {
  assert.equal(model.recommendedResolution(caps()).key, 'm1440');
  const noQhd = caps();
  noQhd.monitor.modes = noQhd.monitor.modes.map((m) =>
    m.height === 1440 ? { ...m, available: false, reason: 'video-memory' } : m
  );
  assert.equal(model.recommendedResolution(noQhd).key, 'm1080');
});

test('a draft is in the resolution of its monitor, whatever its rate', () => {
  const c = caps();
  assert.equal(model.resolutionOf(draft({ monitor: 1440, fps: 60 }), c).key, 'm1440');
  assert.equal(model.resolutionOf(draft({ monitor: 1440, fps: 45 }), c).key, 'm1440');
  // Auto stands for 1920x1080.
  assert.equal(model.resolutionOf(draft({ monitor: 0, fps: 100 }), c).key, 'm1080');
  assert.equal(model.resolutionOf(draft({ portrait: 1920, fps: 75 }), c).key, 'p1920');
});

test('a resolution applies at its fastest rate, and fits portrait limits', () => {
  const c = caps();
  const choice = (key) => model.resolutionChoices(c).find((x) => x.key === key);
  const fhd = model.withResolution(draft({ monitor: 720, fps: 30 }), choice('m1080'), c);
  assert.deepEqual([fhd.monitor, fhd.portrait, fhd.fps], [1080, 0, 100]);
  const qhd = model.withResolution(draft({ portrait: 1920 }), choice('m1440'), c);
  assert.deepEqual([qhd.monitor, qhd.portrait, qhd.fps], [1440, 0, 60]);
  const tall = model.withResolution(
    draft({ transport: 'mjpeg', codec: 'h264' }),
    choice('p2560'),
    c
  );
  assert.deepEqual(
    [tall.portrait, tall.fps, tall.transport, tall.codec],
    [2560, 60, 'direct', 'h265']
  );
  assert.deepEqual({ ...model.draftIssues(tall, c, everything) }, {});
  // A fixed receiver keeps its monitor.
  const fixed = caps({ programmable: false });
  const rate = model.withResolution(
    draft({ monitor: 1080 }),
    model.resolutionChoices(fixed)[0],
    fixed
  );
  assert.deepEqual([rate.monitor, rate.fps], [1080, 60]);
});

test('the monitor refresh follows a lower rate of the resolution', () => {
  const c = caps();
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 100 }), c).refresh, 100);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 60 }), c).refresh, 60);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 50 }), c).refresh, 60);
  assert.equal(model.monitorTarget(draft({ monitor: 1080, fps: 60 }), caps({ follows: false })).refresh, 100);
});

test('a slower stream rate rewrites the monitor only when it follows the rate', () => {
  const saved = draft({ monitor: 1080, fps: 60 });
  assert.equal(model.draftChanges(saved, { ...saved, fps: 30 }, caps()).monitorRewrite, true);
  assert.equal(model.draftChanges(saved, { ...saved, fps: 50 }, caps()).monitorRewrite, false);
  assert.equal(
    model.draftChanges(saved, { ...saved, fps: 30 }, caps({ follows: false })).monitorRewrite,
    false
  );
  assert.equal(model.draftChanges(saved, { ...saved, codec: 'h264' }, caps()).reconnect, true);
});

test('the request carries changed shared settings only', () => {
  const saved = draft();
  assert.deepEqual(
    { ...model.settingsRequest(saved, { ...saved, fps: 30, directPlayback: 'immediate' }) },
    { fps: 30 }
  );
  assert.deepEqual({ ...model.settingsRequest(saved, { ...saved, codec: 'h264' }) }, { type: 1 });
  // The transport is also the device transport: WebRTC carries the same codec.
  assert.deepEqual({ ...model.settingsRequest(saved, { ...saved, transport: 'webrtc' }) }, { transport: 'webrtc' });
  assert.deepEqual(
    { ...model.settingsRequest(saved, { ...saved, portrait: 2560 }) },
    { portraitResolution: 2560, portrait: true }
  );
  assert.deepEqual(
    { ...model.settingsRequest({ ...saved, portrait: 1920 }, { ...saved, portrait: 2560 }) },
    { portraitResolution: 2560 }
  );
  assert.deepEqual(
    { ...model.settingsRequest({ ...saved, portrait: 1920 }, saved) },
    { portrait: false }
  );
});

test('the measured input rate is shown as its nominal rate', () => {
  assert.equal(model.nominalRate(51), 50);
  assert.equal(model.nominalRate(29), 30);
  assert.equal(model.nominalRate(59), 60);
  assert.equal(model.nominalRate(44), 44);
});

test('portrait refresh follows the frame rate like landscape', () => {
  const c = caps();
  assert.equal(model.monitorTarget(draft({ portrait: 1920, fps: 100 }), c).refresh, 100);
  assert.equal(model.monitorTarget(draft({ portrait: 1920, fps: 30 }), c).refresh, 30);
  assert.equal(model.monitorTarget(draft({ portrait: 2560, fps: 45 }), c).refresh, 50);
  const saved = draft({ portrait: 1920, fps: 100 });
  assert.equal(model.draftChanges(saved, { ...saved, fps: 60 }, c).monitorRewrite, true);
});

test('the automatic bitrate follows the size and rate', () => {
  assert.equal(model.autoBitrate(2560, 1440, 60, 'h265'), 20000);
  assert.equal(model.autoBitrate(3840, 2160, 30, 'h265'), 20000);
  assert.equal(model.autoBitrate(1920, 1080, 100, 'h265'), 20000);
  assert.equal(model.autoBitrate(1920, 1080, 60, 'h265'), 12000);
  assert.equal(model.autoBitrate(1920, 1080, 30, 'h265'), 8000);
  assert.equal(model.autoBitrate(1280, 720, 120, 'h265'), 10000);
  assert.equal(model.autoBitrate(1920, 1080, 60, 'h264'), 15000);
  assert.equal(model.autoBitrate(2560, 1440, 60, 'h264'), 20000);
});

test('the automatic bitrate of a draft uses the monitor it writes', () => {
  const c = caps({ input: [1920, 1080] });
  assert.equal(model.draftAutoBitrate(draft({ monitor: 1440, fps: 60 }), c), 20000);
  assert.equal(model.draftAutoBitrate(draft({ monitor: 1440, fps: 60, height: 720 }), c), 5000);
  assert.equal(model.draftAutoBitrate(draft({ monitor: 720, fps: 120 }), c), 10000);
});

test('the automatic bitrate of a draft uses the frame rate of the mode it switches to', () => {
  // From a 3840x2160 input (30 fps) to 2560x1440 at 60 fps: the QHD rate.
  const c = caps({ input: [3840, 2160] });
  assert.equal(model.draftAutoBitrate(draft({ monitor: 1440, fps: 60 }), c), 20000);
  assert.equal(model.draftAutoBitrate(draft({ monitor: 1440, fps: 60, codec: 'h264' }), c), 20000);
});

test('a transport is offered with a codec that plays, or not at all', () => {
  const c = caps();
  const noWebrtcH265 = { ...everything, webrtcH265: false };
  // Direct H.265 to WebRTC without WebRTC H.265 here: an administrator gets
  // WebRTC with H.264.
  const webrtc = model.transportChoice(draft(), 'webrtc', c, noWebrtcH265, true);
  assert.equal(webrtc.transport, 'webrtc');
  assert.equal(webrtc.codec, 'h264');
  assert.equal(model.transportChoice(draft(), 'webrtc', c, everything, true).codec, 'h265');
  // The 1440x2560 portrait profile plays H.265 only: no H.264 fallback.
  assert.equal(
    model.transportChoice(draft({ portrait: 2560 }), 'webrtc', c, noWebrtcH265, true),
    undefined
  );
  // Nor MJPEG.
  assert.equal(
    model.transportChoice(draft({ portrait: 2560 }), 'mjpeg', c, everything, true),
    undefined
  );
  assert.equal(model.transportChoice(draft(), 'mjpeg', c, everything, true).transport, 'mjpeg');
  assert.equal(
    model.transportChoice(draft(), 'direct', c, { ...everything, direct: false }, true),
    undefined
  );
});

test('a user, who cannot change the shared codec, gets no codec fallback', () => {
  const c = caps();
  const noWebrtcH265 = { ...everything, webrtcH265: false };
  assert.equal(model.transportChoice(draft(), 'webrtc', c, noWebrtcH265, false), undefined);
  assert.equal(model.transportChoice(draft(), 'webrtc', c, everything, false).codec, 'h265');
  assert.equal(
    model.transportChoice(draft({ codec: 'h264' }), 'webrtc', c, noWebrtcH265, false).codec,
    'h264'
  );
  assert.equal(model.transportChoice(draft(), 'mjpeg', c, noWebrtcH265, false).transport, 'mjpeg');
});

test('MJPEG is blocked while the HDMI input is 3840x2160', () => {
  const issue = (draftOver, capsOver) =>
    model.draftIssues(draft({ transport: 'mjpeg', ...draftOver }), caps(capsOver), everything)
      .transport;
  const uhd = { input: [3840, 2160] };
  // The stream limit does not matter: a limited 4K input produces no JPEG frames.
  for (const height of [0, 2160, 1440, 1080, 720, 600])
    assert.equal(issue({ height }, uhd), 'mjpeg-4k');
  assert.equal(issue({}, { input: [2560, 1440] }), undefined);
  assert.equal(issue({ height: 2160 }, { input: [1920, 1080] }), undefined);
  assert.equal(issue({}, { input: [0, 0] }), undefined);
  // The codecs are not affected, and an older server that reports nothing never blocks.
  assert.equal(model.draftIssues(draft({ height: 0 }), caps(uhd), everything).transport, undefined);
  assert.equal(issue({}, { ...uhd, mjpegMaxSide: 0 }), undefined);
  assert.equal(model.mjpegBlocked(draft({ transport: 'direct' }), caps(uhd)), true);
});

test('MJPEG follows the monitor the draft writes', () => {
  const c = caps({ input: [1920, 1080] });
  assert.equal(model.mjpegBlocked(draft({ monitor: 2160 }), c), true);
  assert.equal(model.mjpegBlocked(draft({ monitor: 2160, height: 1440 }), c), true);
  assert.equal(model.mjpegBlocked(draft({ monitor: 1440 }), c), false);
  // The current input counts while the monitor is not changed or cannot be.
  const fourK = caps({ input: [3840, 2160] });
  assert.equal(model.mjpegBlocked(draft({ monitor: 1080 }), fourK), false);
  assert.equal(model.mjpegBlocked(draft({ monitor: 0 }), fourK), true);
  assert.equal(
    model.mjpegBlocked(
      draft({ monitor: 1080 }),
      caps({ input: [3840, 2160], programmable: false })
    ),
    true
  );
  assert.equal(model.mjpegBlocked(draft({ monitor: 2160, portrait: 1920 }), c), false);
});

// A device that has not restarted since the update to the shared video memory
// still runs a fixed carveout: the server marks what it cannot capture
// "video-memory". Those choices are simply unavailable; applying never
// restarts the device.
test('what the booted video memory cannot capture is blocked without a restart', () => {
  const c = caps({ uhd: false });
  c.monitor.portrait.profiles = c.monitor.portrait.profiles.map((p) => ({
    ...p,
    available: false,
    reason: 'video-memory'
  }));
  const saved = draft({ monitor: 1080 });
  const next = draft({ monitor: 2160 });
  assert.deepEqual(Object.keys(model.draftChanges(saved, next, c)).sort(), [
    'keys',
    'monitorRewrite',
    'reconnect'
  ]);
  assert.equal(model.draftIssues(next, c, everything).monitor, 'video-memory');
  assert.equal(
    model.draftIssues(draft({ portrait: 1920 }), c, everything).portrait,
    'video-memory'
  );
  const choices = model.resolutionChoices(c);
  assert.deepEqual(
    [...choices.filter((choice) => !choice.available).map((choice) => [choice.key, choice.reason])],
    [
      ['m2160', 'video-memory'],
      ['p1920', 'video-memory'],
      ['p2560', 'video-memory']
    ]
  );
});
