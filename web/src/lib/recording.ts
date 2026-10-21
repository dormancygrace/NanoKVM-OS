import type { RecordingResult } from './recording-result.ts';

// Browser-side recording inspired by sipeed/NanoKVM#682.
// Serialize file writes and wait for the final MediaRecorder chunk before closing.
export interface RecordingWriter {
  write(data: Blob): Promise<void>;
  close(): Promise<void>;
  abort(): Promise<void>;
}

export function recordStream(
  stream: MediaStream,
  writer: RecordingWriter,
  mimeType: string,
  maxPendingBytes = 16 * 1024 * 1024
) {
  const recorder = new MediaRecorder(stream, { mimeType, audioBitsPerSecond: 192_000 });
  let pendingBytes = 0;
  let writes = Promise.resolve();
  let failure: Error | undefined;
  let stopped = false;
  let limited = false;
  let resolve!: (result: RecordingResult) => void;
  let reject!: (error: Error) => void;
  const done = new Promise<RecordingResult>((ok, fail) => {
    resolve = ok;
    reject = fail;
  });
  const stop = (error?: Error) => {
    failure ??= error;
    if (recorder.state !== 'inactive') recorder.stop();
  };
  const ended = () => stop();
  const tracks = stream.getTracks();
  // The video ends the recording; sound that stops (the user turns it off)
  // leaves the rest of the recording silent.
  const videoTracks = tracks.filter((track) => track.kind !== 'audio');
  videoTracks.forEach((track) => track.addEventListener('ended', ended));
  recorder.ondataavailable = (event) => {
    if (!event.data.size || failure) return;
    pendingBytes += event.data.size;
    if (pendingBytes > maxPendingBytes && !limited) {
      limited = true;
      stop();
    }
    writes = writes
      .then(async () => {
        if (!failure) await writer.write(event.data);
      })
      .catch((error: unknown) => {
        stop(error instanceof Error ? error : new Error(String(error)));
      })
      .finally(() => {
        pendingBytes -= event.data.size;
      });
  };
  recorder.onerror = () => stop(new Error('recording-encoder-error'));
  recorder.onstop = async () => {
    if (stopped) return;
    stopped = true;
    videoTracks.forEach((track) => track.removeEventListener('ended', ended));
    tracks.forEach((track) => track.stop());
    await writes;
    try {
      if (failure) {
        await writer.abort();
        reject(failure);
      } else {
        await writer.close();
        resolve({ reason: limited ? 'buffer-limit' : undefined });
      }
    } catch (error) {
      // A rejected close must not leave an open file transaction.
      await writer.abort().catch(() => {});
      reject(error instanceof Error ? error : new Error(String(error)));
    }
  };
  try {
    recorder.start(1000);
  } catch (error) {
    videoTracks.forEach((track) => track.removeEventListener('ended', ended));
    throw error;
  }
  return { stop, done };
}

export function recordingMimeType(withAudio = false): string | null {
  if (typeof MediaRecorder === 'undefined') return null;
  const types = withAudio
    ? ['video/webm;codecs=vp8,opus', 'video/webm', 'video/mp4']
    : ['video/webm;codecs=vp8', 'video/webm', 'video/mp4'];
  return types.find((type) => MediaRecorder.isTypeSupported(type)) ?? null;
}

// Adds a copy of the live USB audio to a recording stream. The copy belongs to
// the recorder; the speakers keep the original.
export function withRecordingAudio(stream: MediaStream, audio: MediaStream | null) {
  audio
    ?.getAudioTracks()
    .filter((track) => track.readyState === 'live')
    .forEach((track) => stream.addTrack(track.clone()));
  return stream;
}

// The returned stream belongs to the recorder. Never stop the live WebRTC tracks.
export function captureRecordingSource(element: HTMLElement, fps: number) {
  if (element instanceof HTMLVideoElement) {
    const video = element as HTMLVideoElement & { captureStream?: () => MediaStream };
    if (!video.captureStream || video.readyState < 2) throw new Error('recording-no-video');
    const captured = video.captureStream();
    const stream = new MediaStream(captured.getVideoTracks().map((track) => track.clone()));
    if (!stream.getVideoTracks().length) throw new Error('recording-no-video');
    return { stream, dispose: () => stream.getTracks().forEach((track) => track.stop()) };
  }
  if (element instanceof HTMLCanvasElement) {
    if (!element.width || !element.height) throw new Error('recording-no-video');
    const stream = element.captureStream(fps);
    return { stream, dispose: () => stream.getTracks().forEach((track) => track.stop()) };
  }
  if (element instanceof HTMLImageElement && element.complete && element.naturalWidth) {
    const canvas = document.createElement('canvas');
    canvas.width = element.naturalWidth;
    canvas.height = element.naturalHeight;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('recording-no-video');
    context.drawImage(element, 0, 0, canvas.width, canvas.height);
    const stream = canvas.captureStream(fps);
    const timer = window.setInterval(() => {
      if (element.complete && element.naturalWidth)
        context.drawImage(element, 0, 0, canvas.width, canvas.height);
    }, 1000 / fps);
    return {
      stream,
      dispose: () => {
        window.clearInterval(timer);
        stream.getTracks().forEach((track) => track.stop());
      }
    };
  }
  throw new Error('recording-no-video');
}
