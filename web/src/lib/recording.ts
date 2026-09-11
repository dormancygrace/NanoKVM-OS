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
  const recorder = new MediaRecorder(stream, { mimeType });
  let pendingBytes = 0;
  let writes = Promise.resolve();
  let failure: Error | undefined;
  let stopped = false;
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const done = new Promise<void>((ok, fail) => {
    resolve = ok;
    reject = fail;
  });
  const stop = (error?: Error) => {
    failure ??= error;
    if (recorder.state !== 'inactive') recorder.stop();
  };
  const ended = () => stop();
  const tracks = stream.getTracks();
  tracks.forEach((track) => track.addEventListener('ended', ended));
  recorder.ondataavailable = (event) => {
    if (!event.data.size || failure) return;
    pendingBytes += event.data.size;
    if (pendingBytes > maxPendingBytes) {
      stop(new Error('recording-backpressure'));
      return;
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
    tracks.forEach((track) => {
      track.removeEventListener('ended', ended);
      track.stop();
    });
    await writes;
    try {
      if (failure) {
        await writer.abort();
        reject(failure);
      } else {
        await writer.close();
        resolve();
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
    tracks.forEach((track) => track.removeEventListener('ended', ended));
    throw error;
  }
  return { stop, done };
}

export function recordingMimeType(): string | null {
  if (typeof MediaRecorder === 'undefined') return null;
  return (
    ['video/webm;codecs=vp8', 'video/webm', 'video/mp4'].find((type) =>
      MediaRecorder.isTypeSupported(type)
    ) ?? null
  );
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
