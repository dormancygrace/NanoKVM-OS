import { RtpClock, type RecordingEnd, type RecordingSource } from '@/lib/recording-source.ts';

import type { ReceivedFrame } from './webrtc-record.worker.ts';
import RecordWorker from './webrtc-record.worker.ts?worker';

export function canRecordReceiver(): boolean {
  return typeof RTCRtpScriptTransform !== 'undefined';
}

/*
 * A recording source for a WebRTC video receiver. The receiver gets an
 * encoded transform before the connection starts (Chrome does not route the
 * frames of a running receiver through a transform set later); it passes the
 * frames on to the decoder unchanged and, while a recording runs, posts a
 * copy here, asking for a keyframe when the recording starts. The size comes
 * from the frame metadata, or from the picture when the browser leaves it out.
 */
export function receiverRecording(
  receiver: RTCRtpReceiver,
  codec: 'h264' | 'h265',
  pictureSize: () => { width: number; height: number }
): { source: RecordingSource; close: () => void } {
  const worker = new RecordWorker();
  receiver.transform = new RTCRtpScriptTransform(worker);
  let nextId = 1;
  let closed = false;
  let active: {
    id: number;
    onPacket: Parameters<RecordingSource['start']>[0];
    onEnd: (reason: RecordingEnd) => void;
    clock: RtpClock;
    width: number;
    height: number;
  } | null = null;

  worker.onmessage = (event: MessageEvent<ReceivedFrame>) => {
    const frame = event.data;
    const current = active;
    // Frames sent before the worker learned of a stop belong to no recording.
    if (!current || frame.recording !== current.id) return;
    if (frame.width && frame.height) {
      current.width = frame.width;
      current.height = frame.height;
    } else if (frame.key || !current.width) {
      ({ width: current.width, height: current.height } = pictureSize());
    }
    current.onPacket({
      key: frame.key,
      timestamp: current.clock.microseconds(frame.rtpTimestamp),
      width: current.width,
      height: current.height,
      data: new Uint8Array(frame.data)
    });
  };

  const end = (reason: RecordingEnd) => {
    const current = active;
    active = null;
    if (!current) return;
    worker.postMessage({ recording: 0 });
    current.onEnd(reason);
  };

  const source: RecordingSource = {
    codec,
    start: (onPacket, onEnd) => {
      if (closed) {
        onEnd('closed');
        return () => {};
      }
      end('stopped');
      const current = {
        id: nextId++,
        onPacket,
        onEnd,
        clock: new RtpClock(),
        width: 0,
        height: 0
      };
      active = current;
      worker.postMessage({ recording: current.id });
      return () => {
        if (active === current) end('stopped');
      };
    },
    requestKeyframe: () => {
      if (active) worker.postMessage({ recording: active.id });
    }
  };
  // After the connection is closed: a stopped worker would hold the frames.
  const close = () => {
    closed = true;
    end('closed');
    worker.terminate();
  };
  return { source, close };
}
