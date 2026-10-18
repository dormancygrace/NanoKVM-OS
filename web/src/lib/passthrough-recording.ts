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
  // Mediabunny closes its target even on cancel. Only this session may commit
  // the actual file transaction; closing the bridge must not commit a bad file.
  const fileWriter = writable.getWriter();
  let retainedBytes = 0;
  const target = new WritableStream({
    async write(chunk) {
      await fileWriter.write(chunk);
      retainedBytes = Math.max(0, retainedBytes - chunk.data.byteLength);
    }
  });
  const output = new Output({
    format: new Mp4OutputFormat({ fastStart: 'fragmented' }),
    target: new StreamTarget(target)
  });
  const video = new EncodedVideoPacketSource(source.codec === 'h265' ? 'hevc' : 'avc');
  output.addVideoTrack(video);
  let sound: MediaStreamAudioTrackSource | undefined;
  let audioEnded = false;
  const endAudio = () => {
    audioEnded = true;
    if (output.state === 'started') sound?.close();
  };
  try {
    if (audio?.readyState === 'live' && (await canEncodeAudio('opus'))) {
      sound = new MediaStreamAudioTrackSource(audio as MediaStreamAudioTrack, {
        codec: 'opus',
        bitrate: 128_000,
        onEncodedPacket: (packet) => {
          retainedBytes += packet.data.byteLength;
          checkBuffer();
        }
      });
      output.addAudioTrack(sound);
      // Closing the source tells the interleaver not to wait for more audio.
      void sound.errorPromise.catch(endAudio);
      audio.addEventListener('ended', endAudio);
      audio.addEventListener('mute', endAudio);
      audioEnded = audio.readyState !== 'live' || audio.muted;
    }
  } catch (error) {
    await output.cancel().catch(() => {});
    audio?.stop();
    await fileWriter.abort().catch(() => {});
    fileWriter.releaseLock();
    throw error;
  }

  const timeline = new PacketTimeline(source.codec);
  const codec = source.codec === 'h265' ? DIRECT_H265_CODEC : DIRECT_H264_CODEC;
  let chain = Promise.resolve();
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
  const enqueue = (task: () => Promise<void>) => {
    chain = chain
      .then(async () => {
        if (!failure) await task();
      })
      .catch((error: unknown) => {
        failure ??= error instanceof Error ? error : new Error(String(error));
        finish();
      });
  };
  const onPacket = (packet: DirectPacket) => {
    if (finished) return;
    const dropped = timeline.dropped;
    const result = timeline.push(packet);
    if (result === 'size-changed') {
      finish();
      return;
    }
    if (timeline.started && timeline.dropped === dropped) {
      // Include packets already accepted by the muxer, not only our promise
      // queue: an absent audio/video track can stall fragment interleaving.
      retainedBytes += packet.data.byteLength;
      if (!checkBuffer()) return;
    }
    if (timeline.started && !started) {
      started = true;
      // Audio capture starts here too, so both tracks begin at this frame.
      enqueue(async () => {
        await output.start();
        if (audioEnded) endAudio();
        markStarted();
      });
    }
    if (!result) return;
    enqueue(() => add(result));
  };
  // A disposed source may end synchronously during subscription.
  let stopSource = () => {};
  try {
    stopSource = source.start(onPacket, () => finish());
    if (finished) stopSource();
  } catch (error) {
    failure = error instanceof Error ? error : new Error(String(error));
    finish();
  }

  function checkBuffer() {
    if (retainedBytes <= maxPendingBytes) return true;
    failure ??= new Error('recording-backpressure');
    finish();
    return false;
  }

  function finish() {
    if (finished) return;
    finished = true;
    stopSource();
    audio?.removeEventListener('ended', endAudio);
    audio?.removeEventListener('mute', endAudio);
    chain = chain
      .then(async () => {
        if (!started) failure ??= new Error('recording-no-video');
        if (failure) throw failure;
        const last = timeline.flush();
        if (last) await add(last);
        await output.finalize();
        await fileWriter.close();
      })
      .catch(async (error: unknown) => {
        failure ??= error instanceof Error ? error : new Error(String(error));
        await output.cancel().catch(() => {});
        await fileWriter.abort().catch(() => {});
      })
      .then(() => {
        audio?.stop();
        fileWriter.releaseLock();
        if (failure) rejectDone(failure);
        else resolveDone();
      });
  }

  return { stop: finish, started: startedPromise, done };
}
