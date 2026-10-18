// Recording the Direct stream as received: the player publishes a source that
// hands over the H.264/H.265 frames it decodes, and PacketTimeline turns them
// into the packets of one MP4 video track.

// One frame of the Direct stream: Annex B data, the device timestamp in
// microseconds and the picture size the player shows.
export type DirectPacket = {
  key: boolean;
  timestamp: number;
  width: number;
  height: number;
  data: Uint8Array;
};

export type DirectRecordingEnd = 'stopped' | 'closed' | 'stream-error';

export type DirectRecordingSource = {
  codec: 'h264' | 'h265';
  // Starts handing over frames (from a requested keyframe on); returns stop.
  start: (
    onPacket: (packet: DirectPacket) => void,
    onEnd: (reason: DirectRecordingEnd) => void
  ) => () => void;
};

let current: DirectRecordingSource | null = null;
const watchers = new Set<(source: DirectRecordingSource | null) => void>();

export function publishDirectRecording(source: DirectRecordingSource | null) {
  if (current === source) return;
  current = source;
  watchers.forEach((watch) => watch(source));
}

export function getDirectRecording(): DirectRecordingSource | null {
  return current;
}

export function watchDirectRecording(
  watch: (source: DirectRecordingSource | null) => void
): () => void {
  watchers.add(watch);
  return () => {
    watchers.delete(watch);
  };
}

export type TimedPacket = {
  key: boolean;
  seconds: number;
  duration: number;
  width: number;
  height: number;
  data: Uint8Array;
};

// The parameter sets (H.264 SPS and PPS; H.265 VPS, SPS and PPS) of an
// Annex B frame, as one string; empty when the frame has none.
export function parameterSets(data: Uint8Array, codec: 'h264' | 'h265'): string {
  const sets: string[] = [];
  let start = -1;
  const take = (end: number) => {
    if (start < 0 || start >= end) return;
    const type = codec === 'h265' ? (data[start] >> 1) & 0x3f : data[start] & 0x1f;
    const wanted = codec === 'h265' ? type >= 32 && type <= 34 : type === 7 || type === 8;
    if (wanted) sets.push(Array.from(data.subarray(start, end), (b) => b.toString(16)).join(','));
  };
  for (let i = 0; i + 2 < data.length; i++) {
    if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 1) {
      take(i > 0 && data[i - 1] === 0 ? i - 1 : i);
      start = i + 3;
      i += 2;
    }
  }
  take(data.length);
  return sets.join('|');
}

/*
 * The packets of one MP4 track from Direct frames: the track starts at the
 * first keyframe with a known size and counts from zero in seconds; a frame
 * that goes back in time is dropped and counted; each packet lasts until the
 * next one, so packets come out one frame late. A keyframe with other
 * parameter sets (a new resolution or profile) or a new picture size ends the
 * track before that frame: the player learns the new size only after decoding
 * it, the parameter sets tell at once.
 */
export class PacketTimeline {
  dropped = 0;
  width = 0;
  height = 0;
  private base: number | null = null;
  private config = '';
  private pending: (DirectPacket & { seconds: number }) | null = null;
  private lastSeconds = -1;
  private lastDuration = 1 / 60;

  constructor(private readonly codec: 'h264' | 'h265' = 'h264') {}

  get started() {
    return this.base !== null;
  }

  push(packet: DirectPacket): TimedPacket | 'size-changed' | null {
    if (this.base === null) {
      if (!packet.key || !packet.width || !packet.height) return null;
      this.base = packet.timestamp;
      this.width = packet.width;
      this.height = packet.height;
      this.config = parameterSets(packet.data, this.codec);
    } else if (packet.width !== this.width || packet.height !== this.height) {
      return 'size-changed';
    } else if (packet.key) {
      const config = parameterSets(packet.data, this.codec);
      if (config && this.config && config !== this.config) return 'size-changed';
    }
    const seconds = (packet.timestamp - this.base) / 1e6;
    if (seconds <= this.lastSeconds) {
      this.dropped++;
      return null;
    }
    this.lastSeconds = seconds;
    const previous = this.pending;
    this.pending = { ...packet, seconds };
    if (!previous) return null;
    this.lastDuration = seconds - previous.seconds;
    return this.timed(previous, this.lastDuration);
  }

  // The last packet, with the duration of the one before it.
  flush(): TimedPacket | null {
    const last = this.pending;
    this.pending = null;
    return last ? this.timed(last, this.lastDuration) : null;
  }

  private timed(packet: DirectPacket & { seconds: number }, duration: number): TimedPacket {
    return {
      key: packet.key,
      seconds: packet.seconds,
      duration,
      width: packet.width,
      height: packet.height,
      data: packet.data
    };
  }
}
