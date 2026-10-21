// Records the Direct or WebRTC stream into MP4 without re-encoding: the H.264/H.265
// frames go into the file as received, the USB audio (if any) is encoded to
// Opus only when encoded receiver packets are unavailable. MP4 fragmentation
// bounds muxer memory; the browser file transaction still commits only on close.
// Loaded on demand.
import {
  canEncodeAudio,
  EncodedAudioPacketSource,
  EncodedPacket,
  EncodedVideoPacketSource,
  MediaStreamAudioTrackSource,
  Mp4OutputFormat,
  Output,
  StreamTarget
} from 'mediabunny';

import { DIRECT_H264_CODEC, DIRECT_H265_CODEC } from '@/lib/encoder.ts';
import {
  AUDIO_RESYNC_SECONDS,
  AudioTimeline,
  OPUS_FRAME_SECONDS,
  OPUS_SILENCE,
  type RecordingAudioSource
} from '@/lib/recording-audio.ts';
import type { RecordingResult } from '@/lib/recording-result.ts';
import {
  PacketTimeline,
  type RecordingPacket,
  type RecordingSource,
  type TimedPacket
} from '@/lib/recording-source.ts';

export type PassthroughRecording = {
  stop: () => void;
  stopAudio: () => void;
  // Resolves when the first keyframe has started the file.
  started: Promise<void>;
  done: Promise<RecordingResult>;
};

// Fragments start at keyframes only, so the recording asks for one when the
// last is this old. The byte limit also bounds retention if the sender does
// not answer a keyframe request. This is not a disk durability guarantee.
const KEYFRAME_INTERVAL = 5;
// Received audio this far behind the video is filled with silence. Longer
// than the re-anchoring threshold, so audio resuming after the gap lands after
// the silence and is kept.
export const SILENCE_LAG = AUDIO_RESYNC_SECONDS + 1;

export async function recordPassthrough(
  source: RecordingSource,
  writable: FileSystemWritableFileStream,
  audio: MediaStreamTrack | null,
  maxPendingBytes = 32 * 1024 * 1024,
  encodedAudio: RecordingAudioSource | null = null
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
  let sound: MediaStreamAudioTrackSource | EncodedAudioPacketSource | undefined;
  let stopAudio = () => {};
  let audioTimeline: AudioTimeline | undefined;
  let firstAudio = true;
  // Set at once when received audio ends; closing the track is queued, and a
  // packet added after it would fail the whole file.
  let audioStopped = false;
  let reason: RecordingResult['reason'];
  let audioEnded = false;
  const endAudio = () => {
    audioEnded = true;
    if (output.state === 'started') sound?.close();
  };
  try {
    if (encodedAudio) {
      sound = new EncodedAudioPacketSource('opus');
      output.addAudioTrack(sound);
    } else if (audio?.readyState === 'live' && (await canEncodeAudio('opus'))) {
      sound = new MediaStreamAudioTrackSource(audio as MediaStreamAudioTrack, {
        codec: 'opus',
        bitrate: 192_000,
        onEncodedPacket: (packet) => {
          retainedBytes += packet.data.byteLength;
          checkBuffer();
        }
      });
      output.addAudioTrack(sound);
      // Closing the source tells the interleaver not to wait for more audio.
      void sound.errorPromise.catch(() => {
        reason ??= 'audio-error';
        endAudio();
      });
      audio.addEventListener('ended', endAudio);
      audioEnded = audio.readyState !== 'live';
    } else if (audio) {
      reason = 'audio-error';
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
  let lastKeyframe = 0;
  let lastRequest = 0;
  let finished = false;
  let markStarted!: () => void;
  const startedPromise = new Promise<void>((resolve) => {
    markStarted = resolve;
  });
  let resolveDone!: (result: RecordingResult) => void;
  let rejectDone!: (error: Error) => void;
  const done = new Promise<RecordingResult>((resolve, reject) => {
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
  const onPacket = (packet: RecordingPacket) => {
    if (finished) return;
    // Refuse the next packet before changing the timeline. Accepted packets
    // can still be drained and finalized into a playable partial recording.
    if (timeline.started && retainedBytes + packet.data.byteLength > maxPendingBytes) {
      reason ??= 'buffer-limit';
      finish();
      return;
    }
    const dropped = timeline.dropped;
    const result = timeline.push(packet);
    if (result === 'size-changed') {
      reason ??= 'source-changed';
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
      enqueue(async () => {
        await output.start();
        if (audioEnded) endAudio();
        markStarted();
      });
      // Use a shared receiver arrival clock for the initial A/V offset, then
      // keep each RTP clock (including gaps) instead of timing every packet by JS.
      if (encodedAudio) {
        audioTimeline = new AudioTimeline(
          packet.receivedAt ?? performance.timeOrigin + performance.now()
        );
        stopAudio = encodedAudio.start(
          (audioPacket) => {
            if (finished || audioStopped || !audioTimeline) return;
            const seconds = audioTimeline.push(audioPacket);
            if (seconds === null) return;
            if (retainedBytes + audioPacket.data.byteLength > maxPendingBytes) {
              reason ??= 'buffer-limit';
              finish();
              return;
            }
            addAudio(audioPacket.data, seconds);
          },
          (endReason) => {
            if (endReason === 'stream-error' || endReason === 'closed') reason ??= 'audio-error';
            audioStopped = true;
            // Queue closing after previously accepted audio packets.
            enqueue(async () => endAudio());
          }
        );
        if (finished) stopAudio();
      }
    }
    if (!result) return;
    if (audioTimeline && !audioStopped) {
      for (const seconds of audioTimeline.silence(result.seconds - SILENCE_LAG))
        addAudio(OPUS_SILENCE, seconds);
    }
    if (result.key) lastKeyframe = result.seconds;
    else if (
      result.seconds - lastKeyframe >= KEYFRAME_INTERVAL &&
      result.seconds - lastRequest >= KEYFRAME_INTERVAL
    ) {
      lastRequest = result.seconds;
      source.requestKeyframe();
    }
    enqueue(() => add(result));
  };
  function addAudio(data: Uint8Array, seconds: number) {
    retainedBytes += data.byteLength;
    const meta = firstAudio
      ? { decoderConfig: { codec: 'opus', sampleRate: 48000, numberOfChannels: 2 } }
      : undefined;
    firstAudio = false;
    enqueue(() =>
      (sound as EncodedAudioPacketSource).add(
        new EncodedPacket(data, 'key', seconds, OPUS_FRAME_SECONDS),
        meta
      )
    );
  }

  // A disposed source may end synchronously during subscription.
  let stopSource = () => {};
  try {
    stopSource = source.start(onPacket, (endReason) => {
      if (endReason !== 'stopped') reason ??= 'stream-error';
      finish();
    });
    if (finished) stopSource();
  } catch (error) {
    failure = error instanceof Error ? error : new Error(String(error));
    finish();
  }

  function checkBuffer() {
    if (retainedBytes <= maxPendingBytes) return true;
    reason ??= 'buffer-limit';
    finish();
    return false;
  }

  function finish() {
    if (finished) return;
    finished = true;
    stopSource();
    stopAudio();
    audio?.removeEventListener('ended', endAudio);
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
        else resolveDone({ reason });
      });
  }

  return {
    stop: finish,
    stopAudio: () => {
      stopAudio();
      audioStopped = true;
      enqueue(async () => endAudio());
    },
    started: startedPromise,
    done
  };
}
