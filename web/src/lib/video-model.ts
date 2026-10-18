// Video settings model: device capabilities, the draft a user edits, video
// modes, validation with reasons, and what applying a draft changes.
// Pure functions without imports, so tests can run them directly.

export type Reason =
  | 'video-memory'
  | 'receiver'
  | 'browser'
  | 'codec'
  | 'transport'
  | 'portrait'
  | 'range'
  | 'mjpeg-4k';

export type MonitorMode = {
  height: number;
  width: number;
  // Auto (height 0): the size it stands for, 0 when it is the stock profile.
  effectiveWidth?: number;
  effectiveHeight?: number;
  rates: number[];
  available: boolean;
  reason?: Reason;
};

export type PortraitProfile = {
  resolution: number;
  width: number;
  rate: number;
  rates?: number[];
  codecs: string[];
  transports: string[];
  available: boolean;
  reason?: Reason;
};

export type StreamLimit = { height: number; width: number; available: boolean; reason?: Reason };
// MJPEG for the current capture; maxSide is the longest input side it can run
// with. Absent on a server that does not restrict it.
export type MjpegCapability = { available: boolean; reason?: Reason; maxSide: number };
export type RateTier = { longSide: number; shortSide: number; fps: number };

export type VideoCapabilities = {
  // receiver: the HDMI receiver chip, e.g. LT6911UXC; absent or empty if unknown.
  input: { width: number; height: number; fps: number; maxFps: number; receiver?: string };
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
  stream: {
    limits: StreamLimit[];
    rateTiers: RateTier[];
    minFps: number;
    maxFps: number;
    mjpeg?: MjpegCapability;
  };
  transports: Record<Transport, string[]>;
  videoMemoryMiB: number;
  // Absent on servers that cannot switch the video memory with a resolution.
  videoMemory?: VideoMemory;
};

// The video memory modes: fixed carveouts that a reboot switches between.
export type VideoMemoryMode = 'fhd' | 'qhd' | 'uhd';
export type VideoMemory = {
  active: string; // the booted mode, or "unknown"
  selected: string; // the mode of the next boot
  modes: { mode: VideoMemoryMode; mib: number; installed: boolean }[];
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
  mjpegChroma: number;
  directPlayback: DirectPlayback;
  frameDetect: boolean;
};

export const FPS_CHOICES = [120, 75, 60, 50, 40, 30];
export const BITRATE_CHOICES = [20000, 15000, 12000, 10000, 8000, 5000, 3000, 2000, 1000];

// The automatic bitrate: a ceiling for motion (video, scrolling), about 0.09
// bit per pixel for H.265 and 1.3 times that for H.264, rounded up to an
// offered value. A still screen sends far less whatever the ceiling: the
// encoder stops at its quality floor. 2560x1440@60 and 3840x2160@30: 20
// Mbit/s; 1920x1080@60: 12; 1280x720@120: 10. Measured on the SG2002 at
// 2560x1440@60, 20 Mbit/s keeps the encoder within its frame time.
export function autoBitrate(width: number, height: number, fps: number, codec: Codec) {
  const kbps = (width * height * fps * 0.09 * (codec === 'h264' ? 1.3 : 1)) / 1000;
  return [...BITRATE_CHOICES].reverse().find((b) => b >= kbps) ?? BITRATE_CHOICES[0];
}

// The economy bitrate: remote access and mobile connections.
export const ECONOMY_BITRATE = 3000;

// The size a monitor setting stands for (Auto included).
export function monitorSize(mode: MonitorMode | undefined) {
  if (!mode) return { width: 0, height: 0 };
  return mode.height
    ? { width: mode.width, height: mode.height }
    : { width: mode.effectiveWidth ?? 0, height: mode.effectiveHeight ?? 0 };
}
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
  const profile = draft.portrait ? portraitProfile(caps, draft.portrait) : undefined;
  const mode = draft.portrait ? undefined : monitorMode(caps, draft.monitor);
  const rates = profile ? (profile.rates ?? [profile.rate]) : (mode?.rates ?? []);
  let refresh = rates[0] ?? 0;
  if (caps.monitor.followsStreamRate) {
    for (const rate of rates) if (rate >= draft.fps) refresh = rate;
  }
  return draft.portrait
    ? { width: profile?.width ?? 0, height: draft.portrait, refresh }
    : { width: mode?.width ?? 0, height: draft.monitor, refresh };
}

// Whether MJPEG cannot run for a draft: its input (the monitor the draft
// writes, else the current one) is longer than the server allows, whatever the
// stream limit is. Mirrors the server, which refuses it.
export function mjpegBlocked(draft: VideoDraft, caps: VideoCapabilities) {
  const maxSide = caps.stream.mjpeg?.maxSide;
  if (!maxSide) return false;
  const changesMonitor =
    !draft.portrait && caps.monitor.programmable && draft.monitor !== caps.monitor.selected;
  const input =
    changesMonitor && draft.monitor
      ? { width: monitorMode(caps, draft.monitor)?.width ?? 0, height: draft.monitor }
      : caps.input;
  return Math.max(input.width, input.height) > maxSide;
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
  if (!issues.transport && draft.transport === 'mjpeg' && mjpegBlocked(draft, caps)) {
    issues.transport = 'mjpeg-4k';
  }
  if (!issues.codec && draft.transport !== 'mjpeg') {
    if (!caps.transports[draft.transport]?.includes(draft.codec)) issues.codec = 'transport';
    else if (!codecPlayable(browser, draft.transport, draft.codec)) issues.codec = 'browser';
  }
  return issues;
}

// Whether a draft plays here: the browser, the transport's codecs and the
// portrait profile accept it (what the settings form checks for these).
export function draftPlayable(draft: VideoDraft, caps: VideoCapabilities, browser: BrowserSupport) {
  const issues = draftIssues(draft, caps, browser);
  return !issues.transport && !issues.codec && !issues.portrait;
}

// The draft that switches to a transport: with the current codec where it
// plays, else (administrators, who set the shared codec) with the other codec,
// else none (the choice is unavailable). A user cannot change the codec:
// applyVideoDraft keeps theirs, so only the current codec is a choice.
export function transportChoice(
  saved: VideoDraft,
  transport: Transport,
  caps: VideoCapabilities,
  browser: BrowserSupport,
  admin: boolean
): VideoDraft | undefined {
  const codecs: Codec[] =
    transport === 'mjpeg' || !admin
      ? [saved.codec]
      : [saved.codec, saved.codec === 'h265' ? 'h264' : 'h265'];
  return codecs
    .map((codec) => ({ ...saved, transport, codec }))
    .find((draft) => draftPlayable(draft, caps, browser));
}

// A resolution: the monitor the computer renders, with the stream rates it
// offers, fastest first. Landscape choices come from the programmable monitor
// profiles, portrait ones from the portrait profiles; with a fixed receiver the
// choice is the current input at the stream rates. The monitor refresh follows
// the chosen rate (monitorTarget), so a resolution and a rate are two choices.
export type ResolutionChoice = {
  key: string;
  monitor: number; // the draft's monitor setting; ignored for portrait choices
  portrait: number;
  width: number;
  height: number;
  rates: number[];
  available: boolean;
  reason?: Reason;
};

export function resolutionKey(kind: 'm' | 'p' | 'i', height: number) {
  return `${kind}${height}`;
}

export function resolutionChoices(caps: VideoCapabilities): ResolutionChoice[] {
  if (!caps.monitor.programmable) {
    const { width, height } = caps.input;
    const ceiling = rateLimit(caps.stream.rateTiers, width, height);
    return [
      {
        key: resolutionKey('i', height),
        monitor: caps.monitor.selected,
        portrait: 0,
        width,
        height,
        rates: FPS_CHOICES.filter((fps) => fps <= ceiling && fps >= caps.stream.minFps),
        available: true
      }
    ];
  }
  const choices: ResolutionChoice[] = [];
  for (const mode of caps.monitor.modes) {
    if (!mode.height) continue; // Auto stands for one of the sizes listed
    choices.push({
      key: resolutionKey('m', mode.height),
      monitor: mode.height,
      portrait: 0,
      width: mode.width,
      height: mode.height,
      rates: [...mode.rates],
      available: mode.available,
      reason: mode.reason
    });
  }
  for (const profile of caps.monitor.portrait.profiles) {
    choices.push({
      key: resolutionKey('p', profile.resolution),
      monitor: caps.monitor.selected,
      portrait: profile.resolution,
      width: profile.width,
      height: profile.resolution,
      rates: [...(profile.rates ?? [profile.rate])],
      available: profile.available,
      reason: profile.reason
    });
  }
  return choices;
}

// The resolution a draft is in.
export function resolutionOf(draft: VideoDraft, caps: VideoCapabilities) {
  let key: string;
  if (!caps.monitor.programmable) key = resolutionKey('i', caps.input.height);
  else if (draft.portrait) key = resolutionKey('p', draft.portrait);
  else {
    const height = draft.monitor || monitorSize(monitorMode(caps, 0)).height;
    key = resolutionKey('m', height);
  }
  return resolutionChoices(caps).find((c) => c.key === key);
}

// The resolution to suggest: 2560x1440, else 1920x1080.
export function recommendedResolution(caps: VideoCapabilities) {
  const choices = resolutionChoices(caps).filter((c) => c.available && !c.portrait);
  for (const [width, height] of [
    [2560, 1440],
    [1920, 1080]
  ]) {
    const choice = choices.find((c) => c.width === width && c.height === height);
    if (choice) return choice;
  }
  return choices[0];
}

// The draft with a resolution applied, at its fastest rate: the receiver's
// limit for that size, which the user may lower. A portrait profile only plays
// some transports and codecs; the draft keeps its own where they fit.
export function withResolution(
  draft: VideoDraft,
  choice: ResolutionChoice,
  caps: VideoCapabilities
): VideoDraft {
  const next = { ...draft, fps: choice.rates[0] ?? draft.fps, portrait: choice.portrait };
  if (caps.monitor.programmable && !choice.portrait) next.monitor = choice.monitor;
  const profile = choice.portrait ? portraitProfile(caps, choice.portrait) : undefined;
  if (profile) {
    if (!profile.transports.includes(next.transport)) next.transport = 'direct';
    if (next.transport !== 'mjpeg' && !profile.codecs.includes(next.codec)) {
      next.codec = profile.codecs.includes('h265') ? 'h265' : 'h264';
    }
  }
  return next;
}

// The capabilities as the draft's resolution will see them: the input becomes
// the monitor the draft writes, else stays the current input.
function prospectiveCaps(draft: VideoDraft, caps: VideoCapabilities): VideoCapabilities {
  const choice = resolutionOf(draft, caps);
  return choice
    ? { ...caps, input: { ...caps.input, width: choice.width, height: choice.height } }
    : caps;
}

// The size a draft streams: its resolution (the monitor it writes), else the
// current input, within the stream limit.
export function draftSize(draft: VideoDraft, caps: VideoCapabilities) {
  return streamSize(draft, prospectiveCaps(draft, caps));
}

// The automatic bitrate of a draft (autoBitrate of what it streams). Size and
// frame-rate limit both come from the resolution the draft switches to, not from the
// current input (a 3840x2160 input would cap a 1440p60 draft at 30 fps).
export function draftAutoBitrate(draft: VideoDraft, caps: VideoCapabilities) {
  const prospective = prospectiveCaps(draft, caps);
  const out = streamSize(draft, prospective);
  return autoBitrate(out.width, out.height, effectiveFps(draft, prospective), draft.codec);
}

// The video memory a draft's monitor needs, in MiB, as the server judges it
// (VideoPool in server/common/video_pool.go): 3840x2160 the UHD pool, 2560x1440
// and every portrait profile the QHD pool. The landscape monitor counts while
// portrait is on: turning portrait off restores it.
export function videoMemoryNeedMiB(draft: Pick<VideoDraft, 'monitor' | 'portrait'>) {
  if (draft.monitor === 2160) return 104;
  if (draft.monitor === 1440 || draft.portrait) return 54;
  return 0;
}

// The smallest installed video memory mode that captures a draft's monitor.
export function videoMemoryFor(
  draft: Pick<VideoDraft, 'monitor' | 'portrait'>,
  caps: VideoCapabilities
): VideoMemoryMode | undefined {
  const need = videoMemoryNeedMiB(draft);
  return caps.videoMemory?.modes
    .filter((m) => m.installed && m.mib >= need)
    .sort((a, b) => a.mib - b.mib)[0]?.mode;
}

export type DraftChange = {
  keys: (keyof VideoDraft)[];
  // The source re-detects the monitor (a short HDMI blackout).
  monitorRewrite: boolean;
  // The page reloads to switch the player.
  reconnect: boolean;
  // Applying switches to this video memory mode and restarts the device: the
  // monitor changes and the smallest installed mode that captures it is not
  // the booted one, larger or smaller.
  memoryRestart?: VideoMemoryMode;
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
      (caps.monitor.followsStreamRate && before.refresh !== after.refresh));
  const reconnect =
    draft.transport !== saved.transport ||
    (draft.transport !== 'mjpeg' && draft.codec !== saved.codec) ||
    (draft.transport === 'direct' && draft.directPlayback !== saved.directPlayback);
  const change: DraftChange = { keys, monitorRewrite, reconnect };
  const active = caps.videoMemory?.active;
  if (
    caps.monitor.programmable &&
    (draft.monitor !== saved.monitor || draft.portrait !== saved.portrait) &&
    active &&
    active !== 'unknown'
  ) {
    const target = videoMemoryFor(draft, caps);
    if (target && target !== active) change.memoryRestart = target;
  }
  return change;
}

// The issues that keep a draft from being applied. The video memory of the
// monitor or the portrait profile does not when applying switches to an
// installed mode that captures it; the portrait profile still decides the
// transport and the codec.
export function blockingIssues(
  saved: VideoDraft,
  draft: VideoDraft,
  caps: VideoCapabilities,
  browser: BrowserSupport
): DraftIssues {
  const issues = draftIssues(draft, caps, browser);
  if (!draftChanges(saved, draft, caps).memoryRestart) return issues;
  if (issues.monitor === 'video-memory') delete issues.monitor;
  if (issues.portrait === 'video-memory') {
    delete issues.portrait;
    const profile = portraitProfile(caps, draft.portrait);
    if (profile && !profile.transports.includes(draft.transport)) issues.transport ??= 'portrait';
    else if (profile && draft.transport !== 'mjpeg' && !profile.codecs.includes(draft.codec))
      issues.codec ??= 'portrait';
  }
  return issues;
}

// The shared part of a draft as a POST /api/vm/video body: changed fields only.
export function settingsRequest(saved: VideoDraft, draft: VideoDraft) {
  const body: Record<string, number | boolean> = {};
  if (streamType(draft) !== streamType(saved)) body.type = streamType(draft);
  if (draft.bitRate !== saved.bitRate) body.bitRate = draft.bitRate;
  if (draft.quality !== saved.quality) body.quality = draft.quality;
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
