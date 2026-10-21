import { registerRecordingAudio, type AudioPacket } from '@/lib/recording-audio.ts';
import { RtpClock, type RecordingEnd } from '@/lib/recording-source.ts';

import type { ReceivedAudio } from './audio-record.worker.ts';
import AudioWorker from './audio-record.worker.ts?worker';

let unavailable = false;
// Install before negotiation, for bundled video audio and standalone USB audio.
export function recordAudioReceiver(receiver: RTCRtpReceiver, onFailure: () => void) {
  if (unavailable || typeof RTCRtpScriptTransform === 'undefined') return null;
  let worker: Worker;
  try {
    worker = new AudioWorker();
  } catch {
    unavailable = true;
    return null;
  }
  let closed = false;
  let next = 1;
  let active: {
    id: number;
    clock: RtpClock;
    packet: (p: AudioPacket) => void;
    end: (r: RecordingEnd) => void;
  } | null = null;
  const end = (reason: RecordingEnd) => {
    const old = active;
    active = null;
    if (!old) return;
    worker.postMessage({ recording: 0 });
    old.end(reason);
  };
  let unregister = () => {};
  const fail = () => {
    if (closed) return;
    unavailable = true;
    closed = true;
    unregister();
    end('stream-error');
    // The owner closes the peer before terminating its transform worker.
    onFailure();
  };
  worker.onerror = fail;
  worker.onmessageerror = fail;
  worker.onmessage = (event: MessageEvent<ReceivedAudio | { type: 'transform-error' }>) => {
    const frame = event.data;
    if ('type' in frame) {
      fail();
      return;
    }
    const session = active;
    if (!session || session.id !== frame.recording) return;
    session.packet({
      data: new Uint8Array(frame.data),
      timestamp: session.clock.microseconds(frame.rtpTimestamp),
      receivedAt: frame.receivedAt
    });
  };
  try {
    receiver.transform = new RTCRtpScriptTransform(worker);
  } catch {
    unavailable = true;
    worker.terminate();
    return null;
  }
  unregister = registerRecordingAudio(receiver.track, {
    start(packet, onEnd) {
      if (closed) {
        onEnd('closed');
        return () => {};
      }
      end('stopped');
      const session = { id: next++, clock: new RtpClock(48000), packet, end: onEnd };
      active = session;
      worker.postMessage({ recording: session.id });
      return () => {
        if (active === session) end('stopped');
      };
    }
  });
  return {
    close() {
      closed = true;
      unregister();
      end('closed');
      worker.terminate();
    }
  };
}
