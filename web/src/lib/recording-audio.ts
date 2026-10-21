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

// Both RTP clocks start at arbitrary values. Anchor the first audio packet to
// the video arrival clock, then use the 48 kHz RTP clock to preserve packet gaps.
export class AudioTimeline {
  private base: number | null = null;
  private offset = 0;
  private last = -1;
  constructor(private readonly videoReceivedAt: number) {}
  push(packet: AudioPacket): number | null {
    if (this.base === null) {
      this.base = packet.timestamp;
      this.offset = (packet.receivedAt - this.videoReceivedAt) / 1000;
    }
    const seconds = this.offset + (packet.timestamp - this.base) / 1e6;
    if (seconds < 0 || seconds <= this.last) return null;
    this.last = seconds;
    return seconds;
  }
}
