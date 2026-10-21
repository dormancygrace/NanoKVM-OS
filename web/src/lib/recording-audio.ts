import type { RecordingEnd } from './recording-source.ts';
import { getUsbAudioStream, watchUsbAudioStream } from './usb-audio-stream.ts';

export type AudioPacket = {
  data: Uint8Array;
  timestamp: number;
  receivedAt: number;
};
export type RecordingAudioSource = {
  start: (packet: (packet: AudioPacket) => void, end: (reason: RecordingEnd) => void) => () => void;
};
const receivers = new WeakMap<MediaStreamTrack, RecordingAudioSource>();
export function registerRecordingAudio(track: MediaStreamTrack, source: RecordingAudioSource) {
  receivers.set(track, source);
  return () => receivers.delete(track);
}

// Bind to the audio the user is listening to, including an explicit speaker stop.
// Muting due to missing RTP data is temporary and must not close this source.
export function getRecordingAudio(stream: MediaStream | null): RecordingAudioSource | null {
  const track = stream?.getAudioTracks().find((track) => track.readyState === 'live');
  const source = track && receivers.get(track);
  if (!source) return null;
  return {
    start(packet, end) {
      let stopped = false;
      let unsubscribe = () => {};
      let detach = () => {};
      const stop = (reason: RecordingEnd) => {
        if (stopped) return;
        stopped = true;
        unsubscribe();
        detach();
        end(reason);
      };
      unsubscribe = watchUsbAudioStream(() => {
        if (getUsbAudioStream() !== stream) stop('stopped');
      });
      if (getUsbAudioStream() !== stream) stop('stopped');
      else detach = source.start(packet, stop);
      if (stopped) detach();
      return () => stop('stopped');
    }
  };
}

// The device sends one 20 ms Opus frame per packet; this one is stereo silence.
export const OPUS_FRAME_SECONDS = 0.02;
export const OPUS_SILENCE = new Uint8Array([0xfc, 0xff, 0xfe]);
// Current servers advance RTP time across capture pauses. Keep this fallback
// for older senders and accumulated shorter gaps: a packet arriving this much
// later than its RTP time starts a new anchor.
export const AUDIO_RESYNC_SECONDS = 0.5;

// Both RTP clocks start at arbitrary values. Anchor the first audio packet to
// the video arrival clock, then use the 48 kHz RTP clock to preserve packet gaps,
// re-anchoring after the sender paused.
export class AudioTimeline {
  private base: number | null = null;
  private offset = 0;
  private last = -1;
  constructor(private readonly videoReceivedAt: number) {}
  push(packet: AudioPacket): number | null {
    const arrival = (packet.receivedAt - this.videoReceivedAt) / 1000;
    let seconds = this.base === null ? arrival : this.offset + (packet.timestamp - this.base) / 1e6;
    if (this.base === null || arrival - seconds > AUDIO_RESYNC_SECONDS) {
      this.base = packet.timestamp;
      this.offset = arrival;
      seconds = arrival;
    }
    if (seconds < 0 || seconds <= this.last) return null;
    this.last = seconds;
    return seconds;
  }
  // Silence frames for the time without packets, up to `until`. The MP4
  // interleaver waits for every open track: without them, a computer playing
  // nothing would hold the video back until the buffer limit ends the file.
  silence(until: number): number[] {
    const frames: number[] = [];
    let next = this.last < 0 ? 0 : this.last + OPUS_FRAME_SECONDS;
    while (next <= until) {
      frames.push(next);
      this.last = next;
      next += OPUS_FRAME_SECONDS;
    }
    return frames;
  }
}
