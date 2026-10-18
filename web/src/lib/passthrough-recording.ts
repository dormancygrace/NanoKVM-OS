// Records the Direct stream into MP4 without re-encoding: the H.264/H.265
// frames go into the file as received, the USB audio (if any) is encoded to
// Opus. Mediabunny writes a fragmented MP4, so a file cut short stays
// playable up to its last fragment. Loaded on demand.
import {
  canEncodeAudio,
  EncodedPacket,
  EncodedVideoPacketSource,
  MediaStreamAudioTrackSource,
  Mp4OutputFormat,
  Output,
  StreamTarget
} from 'mediabunny';

import {
  PacketTimeline,
  type DirectPacket,
  type DirectRecordingSource,
  type TimedPacket
} from '@/lib/direct-recording.ts';
import { DIRECT_H264_CODEC, DIRECT_H265_CODEC } from '@/lib/encoder.ts';

export type PassthroughRecording = {
  stop: () => void;
  // Resolves when the first keyframe has started the file.
  started: Promise<void>;
  done: Promise<void>;
};

export async function recordPassthrough(
  source: DirectRecordingSource,
  writable: FileSystemWritableFileStream,
  audio: MediaStreamTrack | null,
  maxPendingBytes = 32 * 1024 * 1024
): Promise<PassthroughRecording> {
  const output = new Output({
    format: new Mp4OutputFormat({ fastStart: 'fragmented' }),
    target: new StreamTarget(writable as unknown as WritableStream)
  });
  const video = new EncodedVideoPacketSource(source.codec === 'h265' ? 'hevc' : 'avc');
  output.addVideoTrack(video);
  if (audio && (await canEncodeAudio('opus'))) {
    const sound = new MediaStreamAudioTrackSource(audio as MediaStreamAudioTrack, {
      codec: 'opus',
      bitrate: 128_000
    });
    // Sound that fails or ends leaves the video recording.
    sound.errorPromise.catch(() => {});
    output.addAudioTrack(sound);
  }

  const timeline = new PacketTimeline(source.codec);
  const codec = source.codec === 'h265' ? DIRECT_H265_CODEC : DIRECT_H264_CODEC;
  let chain = Promise.resolve();
  let pendingBytes = 0;
  let failure: Error | undefined;
  let started = false;
  let first = true;
  let finished = false;
  let markStarted!: () => void;
  const startedPromise = new Promise<void>((resolve) => {
    markStarted = resolve;
  });
  let resolveDone!: () => void;
  let rejectDone!: (error: Error) => void;
  const done = new Promise<void>((resolve, reject) => {
    resolveDone = resolve;
    rejectDone = reject;
  });

  const add = (packet: TimedPacket) => {
    const meta = first
      ? {
          decoderConfig: { codec, codedWidth: packet.width, codedHeight: packet.height }
        }
      : undefined;
    first = false;
    return video.add(
      new EncodedPacket(packet.data, packet.key ? 'key' : 'delta', packet.seconds, packet.duration),
      meta
    );
  };
  const enqueue = (task: () => Promise<void>, bytes: number) => {
    pendingBytes += bytes;
    chain = chain
      .then(async () => {
        if (!failure) await task();
      })
      .catch((error: unknown) => {
        failure ??= error instanceof Error ? error : new Error(String(error));
        finish();
      })
      .finally(() => {
        pendingBytes -= bytes;
      });
  };
  const onPacket = (packet: DirectPacket) => {
    if (finished) return;
    const result = timeline.push(packet);
    if (result === 'size-changed') {
      finish();
      return;
    }
    if (timeline.started && !started) {
      started = true;
      // Audio capture starts here too, so both tracks begin at this frame.
      enqueue(async () => {
        await output.start();
        markStarted();
      }, 0);
    }
    if (!result) return;
    if (pendingBytes + result.data.byteLength > maxPendingBytes) {
      failure ??= new Error('recording-backpressure');
      finish();
      return;
    }
    enqueue(() => add(result), result.data.byteLength);
  };
  const stopSource = source.start(onPacket, (reason) => {
    if (reason !== 'stopped') finish();
  });

  function finish() {
    if (finished) return;
    finished = true;
    stopSource();
    chain = chain
      .then(async () => {
        if (!started) {
          await writable.abort().catch(() => {});
          failure ??= new Error('recording-no-video');
          return;
        }
        const last = timeline.flush();
        if (last && !failure) await add(last);
        await output.finalize();
      })
      .catch(async (error: unknown) => {
        failure ??= error instanceof Error ? error : new Error(String(error));
        await output.cancel().catch(() => {});
      })
      .then(() => {
        audio?.stop();
        if (failure) rejectDone(failure);
        else resolveDone();
      });
  }

  return { stop: finish, started: startedPromise, done };
}
