// Video settings model: device capabilities, the draft a user edits,
// presets, validation with reasons, and what applying a draft changes.
// Pure functions without imports, so tests can run them directly.

export type Reason =
  'video-memory' | 'receiver' | 'browser' | 'codec' | 'transport' | 'portrait' | 'range';

export type MonitorMode = {
  height: number;
  width: number;
  rates: number[];
  available: boolean;
  reason?: Reason;
};

export type PortraitProfile = {
  resolution: number;
  width: number;
  rate: number;
  codecs: string[];
  transports: string[];
  available: boolean;
  reason?: Reason;
};

export type StreamLimit = { height: number; width: number; available: boolean; reason?: Reason };
export type RateTier = { longSide: number; shortSide: number; fps: number };

export type VideoCapabilities = {
  input: { width: number; height: number; fps: number; maxFps: number };
  monitor: {
    programmable: boolean;
    requiresPowerCycle: boolean;
    powerCyclePending: boolean;
    followsStreamRate: boolean;
    selected: number;
    refreshHz: number;
    modes: MonitorMode[];
    portrait: { enabled: boolean; resolution: number; profiles: PortraitProfile[] };
  };
  stream: { limits: StreamLimit[]; rateTiers: RateTier[]; minFps: number; maxFps: number };
  transports: Record<Transport, string[]>;
  videoMemoryMiB: number;
};

export type Transport = 'direct' | 'webrtc' | 'mjpeg';
export type Codec = 'h264' | 'h265';
export type DirectPlayback = 'paced' | 'immediate';

// What this browser can play; probed once by the page.
export type BrowserSupport = {
  direct: boolean;
  webrtc: boolean;
  directH265: boolean;
  webrtcH265: boolean;
};

export type VideoDraft = {
  monitor: number; // 0 is Auto
  portrait: number; // 0 is landscape, otherwise the portrait profile height
  height: number; // stream limit, 0 is the input size
  fps: number;
  transport: Transport;
  codec: Codec;
  bitRate: number; // kbit/s, H.264/H.265
  quality: number; // percent, MJPEG
  gop: number;
  gopMode: number; // 0 NormalP, 1 SmartP
  mjpegChroma: number;
  directPlayback: DirectPlayback;
  frameDetect: boolean;
};

export const FPS_CHOICES = [120, 75, 60, 50, 40, 30];
export const BITRATE_CHOICES = [20000, 15000, 10000, 5000, 3000, 2000, 1000];
export const QUALITY_CHOICES = [100, 80, 60, 50];

// Server stream type: what the device encodes for this draft.
export function streamType(draft: Pick<VideoDraft, 'transport' | 'codec'>) {
  return draft.transport === 'mjpeg' ? 0 : draft.codec === 'h265' ? 2 : 1;
}

// VI measures a 50 Hz input as 50 or 51 fps; show the nominal rate.
export function nominalRate(fps: number) {
  const nominal = [24, 25, 30, 40, 50, 60, 75, 120].find((rate) => Math.abs(rate - fps) <= 1);
  return nominal ?? fps;
}

export function rateLimit(tiers: RateTier[], width: number, height: number) {
  if (width <= 0 || height <= 0) return 120;
  const longer = Math.max(width, height);
  const shorter = Math.min(width, height);
  for (const tier of tiers) {
    if (tier.longSide === 0 || (longer <= tier.longSide && shorter <= tier.shortSide)) {
      return tier.fps;
    }
  }
  return tiers[0]?.fps ?? 120;
}

export function portraitProfile(caps: VideoCapabilities, resolution: number) {
  return caps.monitor.portrait.profiles.find((p) => p.resolution === resolution);
}

export function monitorMode(caps: VideoCapabilities, height: number) {
  return caps.monitor.modes.find((m) => m.height === height);
}

// The size the encoder produces: the input, or the stream limit when smaller.
export function streamSize(draft: VideoDraft, caps: VideoCapabilities) {
  const { width, height } = caps.input;
  const limit = caps.stream.limits.find((l) => l.height === draft.height);
  if (!width || !height) return { width: limit?.width ?? 0, height: draft.height };
  if (!draft.height || !limit || Math.min(width, height) <= draft.height) return { width, height };
  const portrait = height > width;
  return portrait
    ? { width: draft.height, height: limit.width }
    : { width: limit.width, height: draft.height };
}

// The frame rate the device delivers for a draft: the request within the
// limits of the input and output sizes and of a portrait profile.
export function effectiveFps(draft: VideoDraft, caps: VideoCapabilities) {
  const out = streamSize(draft, caps);
  let fps = Math.min(
    draft.fps,
    rateLimit(caps.stream.rateTiers, caps.input.width, caps.input.height),
    rateLimit(caps.stream.rateTiers, out.width, out.height)
  );
  if (draft.portrait) {
    const profile = portraitProfile(caps, draft.portrait);
    if (profile) fps = Math.min(fps, profile.rate);
  }
  return fps;
}

// The monitor the source will see: size and refresh rate. With a rate-following
// monitor the refresh is the slowest offered rate not below the stream rate.
export function monitorTarget(draft: VideoDraft, caps: VideoCapabilities) {
  if (draft.portrait) {
    const profile = portraitProfile(caps, draft.portrait);
    return { width: profile?.width ?? 0, height: draft.portrait, refresh: profile?.rate ?? 0 };
  }
  const mode = monitorMode(caps, draft.monitor);
  const rates = mode?.rates ?? [];
  let refresh = rates[0] ?? 0;
  if (caps.monitor.followsStreamRate) {
    for (const rate of rates) if (rate >= draft.fps) refresh = rate;
  }
  return { width: mode?.width ?? 0, height: draft.monitor, refresh };
}

export function codecPlayable(browser: BrowserSupport, transport: Transport, codec: Codec) {
  if (transport === 'mjpeg' || codec === 'h264') return true;
  return transport === 'direct' ? browser.directH265 : browser.webrtcH265;
}

export function transportPlayable(browser: BrowserSupport, transport: Transport) {
  return transport === 'mjpeg' || (transport === 'direct' ? browser.direct : browser.webrtc);
}

// Why a choice is unavailable for a draft; undefined when it is fine.
export type DraftIssues = Partial<
  Record<'monitor' | 'portrait' | 'height' | 'fps' | 'transport' | 'codec', Reason>
>;

export function draftIssues(
  draft: VideoDraft,
  caps: VideoCapabilities,
  browser: BrowserSupport
): DraftIssues {
  const issues: DraftIssues = {};
  if (draft.portrait) {
    const profile = portraitProfile(caps, draft.portrait);
    if (!profile || !profile.available) issues.portrait = profile?.reason ?? 'receiver';
    else if (!profile.transports.includes(draft.transport)) issues.transport = 'portrait';
    else if (draft.transport !== 'mjpeg' && !profile.codecs.includes(draft.codec))
      issues.codec = 'portrait';
  } else {
    const mode = monitorMode(caps, draft.monitor);
    if (!mode || !mode.available) issues.monitor = mode?.reason ?? 'receiver';
  }
  const limit = caps.stream.limits.find((l) => l.height === draft.height);
  if (!limit || !limit.available) issues.height = limit?.reason ?? 'video-memory';
  if (
    !Number.isInteger(draft.fps) ||
    draft.fps < caps.stream.minFps ||
    draft.fps > caps.stream.maxFps
  ) {
    issues.fps = 'range';
  }
  if (!issues.transport && !transportPlayable(browser, draft.transport)) {
    issues.transport = 'browser';
  }
  if (!issues.codec && draft.transport !== 'mjpeg') {
    if (!caps.transports[draft.transport]?.includes(draft.codec)) issues.codec = 'transport';
    else if (!codecPlayable(browser, draft.transport, draft.codec)) issues.codec = 'browser';
  }
  return issues;
}

export type PresetId = 'auto' | 'sharp' | 'smooth' | 'responsive' | 'compatible' | 'saver';
export const PRESETS: PresetId[] = ['auto', 'sharp', 'smooth', 'responsive', 'compatible', 'saver'];

type PresetSpec = {
  monitor: number[]; // preferred monitor settings, first available wins
  height: number;
  fps: number | 'max';
  transports: Transport[];
  codec: Codec | 'best';
  bitRate: number;
  gop: number;
  gopMode: number;
  directPlayback: DirectPlayback;
};

const PRESET_SPECS: Record<PresetId, PresetSpec> = {
  auto: {
    monitor: [0],
    height: 0,
    fps: 'max',
    transports: ['direct', 'webrtc', 'mjpeg'],
    codec: 'best',
    bitRate: 5000,
    gop: 30,
    gopMode: 1,
    directPlayback: 'paced'
  },
  sharp: {
    monitor: [2160, 1440, 1080],
    height: 0,
    fps: 'max',
    transports: ['direct', 'webrtc'],
    codec: 'best',
    bitRate: 20000,
    gop: 60,
    gopMode: 1,
    directPlayback: 'paced'
  },
  smooth: {
    monitor: [1080],
    height: 0,
    fps: 75,
    transports: ['direct', 'webrtc'],
    codec: 'best',
    bitRate: 10000,
    gop: 75,
    gopMode: 1,
    directPlayback: 'paced'
  },
  responsive: {
    monitor: [720],
    height: 0,
    fps: 120,
    transports: ['direct', 'webrtc'],
    codec: 'h264',
    bitRate: 5000,
    gop: 120,
    gopMode: 1,
    directPlayback: 'immediate'
  },
  compatible: {
    monitor: [1080],
    height: 0,
    fps: 60,
    transports: ['webrtc', 'mjpeg'],
    codec: 'h264',
    bitRate: 5000,
    gop: 60,
    gopMode: 1,
    directPlayback: 'paced'
  },
  saver: {
    monitor: [1080],
    height: 1080,
    fps: 30,
    transports: ['direct', 'webrtc', 'mjpeg'],
    codec: 'best',
    bitRate: 1000,
    gop: 60,
    gopMode: 1,
    directPlayback: 'paced'
  }
};

// Whether presets may change the monitor: it must be writable without a
// power cycle, and a portrait monitor stays as the user chose it.
export function presetsSetMonitor(caps: VideoCapabilities, current: VideoDraft) {
  return caps.monitor.programmable && !caps.monitor.requiresPowerCycle && !current.portrait;
}

// The draft a preset produces from the current one, or why it cannot apply.
export function buildPreset(
  id: PresetId,
  caps: VideoCapabilities,
  browser: BrowserSupport,
  current: VideoDraft
): { draft: VideoDraft } | { reason: Reason } {
  const spec = PRESET_SPECS[id];
  const draft: VideoDraft = { ...current, height: spec.height, bitRate: spec.bitRate };
  if (presetsSetMonitor(caps, current)) {
    const mode = spec.monitor.map((height) => monitorMode(caps, height)).find((m) => m?.available);
    if (!mode) return { reason: monitorMode(caps, spec.monitor[0])?.reason ?? 'receiver' };
    draft.monitor = mode.height;
  }
  const transport = spec.transports.find((t) => transportPlayable(browser, t));
  if (!transport) return { reason: 'browser' };
  draft.transport = transport;
  draft.codec =
    spec.codec === 'best'
      ? caps.transports[transport]?.includes('h265') && codecPlayable(browser, transport, 'h265')
        ? 'h265'
        : 'h264'
      : spec.codec;
  if (draft.portrait) {
    const profile = portraitProfile(caps, draft.portrait);
    if (profile && !profile.transports.includes(transport)) return { reason: 'portrait' };
    if (profile && transport !== 'mjpeg' && !profile.codecs.includes(draft.codec)) {
      if (!profile.codecs.some((c) => codecPlayable(browser, transport, c as Codec))) {
        return { reason: 'portrait' };
      }
      draft.codec = profile.codecs[0] as Codec;
    }
  }
  // The highest rate the new monitor (or the current input) and the stream
  // limit allow.
  draft.fps = 120;
  let ceiling = effectiveFps(draft, caps);
  const mode = monitorMode(caps, draft.monitor);
  if (presetsSetMonitor(caps, current) && mode) {
    const tiers = caps.stream.rateTiers;
    const limit = caps.stream.limits.find((l) => l.height === draft.height);
    ceiling = Math.min(
      mode.rates[0] ?? 120,
      mode.width ? rateLimit(tiers, mode.width, mode.height) : ceiling,
      limit && limit.height ? rateLimit(tiers, limit.width, limit.height) : 120
    );
  }
  draft.fps = spec.fps === 'max' ? ceiling : Math.min(spec.fps, ceiling);
  draft.gop = spec.gop;
  draft.gopMode = spec.gopMode;
  draft.directPlayback = spec.directPlayback;
  draft.frameDetect = false;
  const issues = draftIssues(draft, caps, browser);
  const reason = Object.values(issues)[0];
  return reason ? { reason } : { draft };
}

const PRESET_FIELDS: (keyof VideoDraft)[] = [
  'monitor',
  'height',
  'fps',
  'transport',
  'codec',
  'bitRate',
  'gop',
  'directPlayback'
];

// The preset a draft equals, or 'custom'.
export function matchPreset(
  draft: VideoDraft,
  caps: VideoCapabilities,
  browser: BrowserSupport
): PresetId | 'custom' {
  for (const id of PRESETS) {
    const built = buildPreset(id, caps, browser, draft);
    if ('draft' in built && PRESET_FIELDS.every((key) => built.draft[key] === draft[key])) {
      return id;
    }
  }
  return 'custom';
}

export type DraftChange = {
  keys: (keyof VideoDraft)[];
  // The source re-detects the monitor (a short HDMI blackout).
  monitorRewrite: boolean;
  // The page reloads to switch the player.
  reconnect: boolean;
};

export function draftChanges(
  saved: VideoDraft,
  draft: VideoDraft,
  caps: VideoCapabilities
): DraftChange {
  const keys = (Object.keys(draft) as (keyof VideoDraft)[]).filter((k) => draft[k] !== saved[k]);
  const before = monitorTarget(saved, caps);
  const after = monitorTarget(draft, caps);
  const monitorRewrite =
    caps.monitor.programmable &&
    (draft.monitor !== saved.monitor ||
      draft.portrait !== saved.portrait ||
      (caps.monitor.followsStreamRate && !draft.portrait && before.refresh !== after.refresh));
  const reconnect =
    draft.transport !== saved.transport ||
    (draft.transport !== 'mjpeg' && draft.codec !== saved.codec) ||
    (draft.transport === 'direct' && draft.directPlayback !== saved.directPlayback);
  return { keys, monitorRewrite, reconnect };
}

// The shared part of a draft as a POST /api/vm/video body: changed fields only.
export function settingsRequest(saved: VideoDraft, draft: VideoDraft) {
  const body: Record<string, number | boolean> = {};
  if (streamType(draft) !== streamType(saved)) body.type = streamType(draft);
  if (draft.bitRate !== saved.bitRate) body.bitRate = draft.bitRate;
  if (draft.quality !== saved.quality) body.quality = draft.quality;
  if (draft.gop !== saved.gop) body.gop = draft.gop;
  if (draft.gopMode !== saved.gopMode) body.gopMode = draft.gopMode;
  if (draft.mjpegChroma !== saved.mjpegChroma) body.mjpegChroma = draft.mjpegChroma;
  if (draft.height !== saved.height) body.height = draft.height;
  if (draft.fps !== saved.fps) body.fps = draft.fps;
  // A portrait profile change rewrites the EDID itself; send the switch only
  // when portrait turns on or off, so the EDID is written once.
  if (draft.portrait && draft.portrait !== saved.portrait) body.portraitResolution = draft.portrait;
  if ((draft.portrait !== 0) !== (saved.portrait !== 0)) body.portrait = draft.portrait !== 0;
  if (draft.monitor !== saved.monitor) body.monitor = draft.monitor;
  return body;
}
