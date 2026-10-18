// The USB audio this page plays, for features that use it besides the
// speakers (video recording). useUsbAudio publishes the stream once it plays
// and clears it when playback stops.

let current: MediaStream | null = null;
const watchers = new Set<(stream: MediaStream | null) => void>();

export function publishUsbAudioStream(stream: MediaStream | null) {
  if (current === stream) return;
  current = stream;
  watchers.forEach((watch) => watch(stream));
}

// The stream while it has a live audio track, otherwise null.
export function getUsbAudioStream(): MediaStream | null {
  return current?.getAudioTracks().some((track) => track.readyState === 'live') ? current : null;
}

export function watchUsbAudioStream(watch: (stream: MediaStream | null) => void): () => void {
  watchers.add(watch);
  return () => {
    watchers.delete(watch);
  };
}
