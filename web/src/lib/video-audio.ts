// USB audio carried by the WebRTC video connection. The video component
// publishes a channel once its peer connection has negotiated the audio
// m-line; the audio hook listens through it (audio-start / audio-stop on the
// video signaling socket) instead of opening a second peer connection. Both
// tracks share one MediaStream id on the server, so the browser keeps them
// in sync. Without a channel (MJPEG, direct video, an older server) the hook
// uses the standalone audio connection.

export type VideoAudioEvent = 'started' | 'stopped' | 'error' | 'closed';

export type VideoAudioChannel = {
  peer: RTCPeerConnection;
  start: () => void;
  stop: () => void;
  // Resolves with the remote audio stream; it may arrive after publication.
  stream: () => Promise<MediaStream>;
  subscribe: (listener: (event: VideoAudioEvent, detail?: string) => void) => () => void;
};

let current: VideoAudioChannel | null = null;
const watchers = new Set<(channel: VideoAudioChannel | null) => void>();

export function publishVideoAudio(channel: VideoAudioChannel | null) {
  if (current === channel) return;
  current = channel;
  watchers.forEach((watch) => watch(channel));
}

export function getVideoAudio(): VideoAudioChannel | null {
  return current;
}

export function watchVideoAudio(watch: (channel: VideoAudioChannel | null) => void): () => void {
  watchers.add(watch);
  return () => {
    watchers.delete(watch);
  };
}

// Builds the channel for one video peer connection. The caller forwards the
// server's audio-* messages to emit() and the remote audio track to setStream().
export function createVideoAudioChannel(
  peer: RTCPeerConnection,
  send: (event: string) => void
): VideoAudioChannel & {
  emit: (event: VideoAudioEvent, detail?: string) => void;
  setStream: (stream: MediaStream) => void;
} {
  const listeners = new Set<(event: VideoAudioEvent, detail?: string) => void>();
  let resolveStream: (stream: MediaStream) => void = () => {};
  let streamValue: MediaStream | null = null;
  const streamPromise = new Promise<MediaStream>((resolve) => {
    resolveStream = resolve;
  });
  return {
    peer,
    start: () => send('audio-start'),
    stop: () => send('audio-stop'),
    stream: () => (streamValue ? Promise.resolve(streamValue) : streamPromise),
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    emit: (event, detail) => listeners.forEach((listener) => listener(event, detail)),
    setStream: (stream) => {
      streamValue = stream;
      resolveStream(stream);
    }
  };
}
