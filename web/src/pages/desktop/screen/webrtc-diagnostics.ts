// Opt-in receiver diagnostics. No SDP, candidate addresses or credentials are exported.
type Stat = Record<string, unknown>;
type StatsReport = {
  forEach(callback: (stat: Stat) => void): void;
  get(id: string): Stat | undefined;
};

const number = (stat: Stat | undefined, key: string): number | null => {
  const value = stat?.[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
};
const string = (stat: Stat | undefined, key: string): string | null =>
  typeof stat?.[key] === 'string' ? (stat[key] as string) : null;

export function readWebrtcCounters(report: StatsReport, quality?: VideoPlaybackQuality) {
  let inbound: Stat | undefined;
  report.forEach((stat) => {
    if (stat.type !== 'inbound-rtp' || (stat.kind ?? stat.mediaType) !== 'video') return;
    if (!inbound || (number(stat, 'bytesReceived') ?? 0) > (number(inbound, 'bytesReceived') ?? 0))
      inbound = stat;
  });
  if (!inbound) return null;
  const linked = (stat: Stat | undefined, key: string) => {
    const id = string(stat, key);
    return id ? report.get(id) : undefined;
  };
  const codec = linked(inbound, 'codecId');
  const transport = linked(inbound, 'transportId');
  const pair = linked(transport, 'selectedCandidatePairId');
  return {
    streamId: string(inbound, 'id'),
    timestampMs: number(inbound, 'timestamp'),
    codec: string(codec, 'mimeType'),
    codecParameters: string(codec, 'sdpFmtpLine'),
    decoder: string(inbound, 'decoderImplementation'),
    width: number(inbound, 'frameWidth'),
    height: number(inbound, 'frameHeight'),
    framesReceived: number(inbound, 'framesReceived'),
    framesDecoded: number(inbound, 'framesDecoded'),
    framesRendered: number(inbound, 'framesRendered'),
    // Playback counters are distinct from RTP decoder/drop counters.
    framesPresented: quality ? quality.totalVideoFrames - quality.droppedVideoFrames : null,
    playbackDroppedFrames: quality?.droppedVideoFrames ?? null,
    framesDropped: number(inbound, 'framesDropped'),
    keyFramesDecoded: number(inbound, 'keyFramesDecoded'),
    bytesReceived: number(inbound, 'bytesReceived'),
    packetsReceived: number(inbound, 'packetsReceived'),
    packetsLost: number(inbound, 'packetsLost'),
    nackCount: number(inbound, 'nackCount'),
    pliCount: number(inbound, 'pliCount'),
    freezeCount: number(inbound, 'freezeCount'),
    totalFreezesDuration: number(inbound, 'totalFreezesDuration'),
    pauseCount: number(inbound, 'pauseCount'),
    totalDecodeTime: number(inbound, 'totalDecodeTime'),
    jitterBufferDelay: number(inbound, 'jitterBufferDelay'),
    jitterBufferEmittedCount: number(inbound, 'jitterBufferEmittedCount'),
    rttMs:
      pair && number(pair, 'currentRoundTripTime') !== null
        ? number(pair, 'currentRoundTripTime')! * 1000
        : null
  };
}

type Counters = NonNullable<ReturnType<typeof readWebrtcCounters>>;
const delta = (current: number | null, previous: number | null) =>
  current !== null && previous !== null && current >= previous ? current - previous : null;

export function summarizeWebrtc(current: Counters, previous: Counters | null) {
  const elapsed =
    previous?.streamId === current.streamId
      ? delta(current.timestampMs, previous.timestampMs)
      : null;
  const seconds = elapsed !== null && elapsed > 0 ? elapsed / 1000 : null;
  const difference = (key: keyof Counters) => {
    const a = current[key],
      b = previous?.[key];
    return seconds !== null && typeof a === 'number' && typeof b === 'number' ? delta(a, b) : null;
  };
  const rate = (key: keyof Counters) => {
    const count = difference(key);
    return count !== null && seconds !== null ? count / seconds : null;
  };
  const meanMs = (sum: keyof Counters, count: keyof Counters) => {
    const duration = difference(sum),
      events = difference(count);
    return duration !== null && events !== null && events > 0 ? (duration * 1000) / events : null;
  };
  const bytesPerSecond = rate('bytesReceived');
  return {
    ...current,
    intervalSeconds: seconds,
    decodedFps: rate('framesDecoded'),
    renderedFps: rate('framesRendered'),
    presentedFps: rate('framesPresented'),
    receivedFps: rate('framesReceived'),
    droppedFrames: difference('framesDropped'),
    bitrateMbps: bytesPerSecond === null ? null : (bytesPerSecond * 8) / 1e6,
    decodeMs: meanMs('totalDecodeTime', 'framesDecoded'),
    jitterBufferMs: meanMs('jitterBufferDelay', 'jitterBufferEmittedCount')
  };
}

export function startWebrtcDiagnostics(
  peer: RTCPeerConnection,
  output: HTMLOutputElement,
  video?: HTMLVideoElement | null
) {
  let stopped = false;
  let pending = false;
  let previous: Counters | null = null;
  const history: Array<ReturnType<typeof summarizeWebrtc> & { visibility: string }> = [];
  const display = (value: number | null) => (value === null ? 'n/a' : value.toFixed(1));
  const sample = async () => {
    if (stopped || pending) return;
    pending = true;
    try {
      const report = await peer.getStats();
      if (stopped) return;
      const quality =
        typeof video?.getVideoPlaybackQuality === 'function'
          ? video.getVideoPlaybackQuality()
          : undefined;
      const counters = readWebrtcCounters(report, quality);
      if (!counters) {
        output.textContent = `WebRTC diagnostics: waiting for video (${peer.connectionState})`;
        return;
      }
      const stats = {
        ...summarizeWebrtc(counters, previous),
        visibility: document.visibilityState
      };
      previous = counters;
      history.push(stats);
      if (history.length > 90) history.shift();
      output.dataset.webrtcStats = JSON.stringify(stats);
      output.dataset.webrtcHistory = JSON.stringify(history);
      output.textContent = `${stats.codec ?? 'WebRTC'}: presented ${display(stats.presentedFps)} FPS, decoded ${display(stats.decodedFps)} FPS; dropped ${stats.framesDropped ?? 'n/a'}, lost ${stats.packetsLost ?? 'n/a'}, freezes ${stats.freezeCount ?? 'n/a'}; buffer ${display(stats.jitterBufferMs)} ms, RTT ${display(stats.rttMs)} ms`;
    } catch {
      if (!stopped) output.textContent = 'WebRTC diagnostics: statistics unavailable';
    } finally {
      pending = false;
    }
  };
  output.textContent = 'WebRTC diagnostics: waiting for video';
  delete output.dataset.webrtcStats;
  delete output.dataset.webrtcHistory;
  const timer = setInterval(() => {
    void sample();
  }, 1000);
  void sample();
  return () => {
    stopped = true;
    clearInterval(timer);
    delete output.dataset.webrtcStats;
    delete output.dataset.webrtcHistory;
    output.textContent = 'WebRTC diagnostics: stopped';
  };
}
