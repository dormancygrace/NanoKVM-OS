// A playable file may be shorter than requested. Report that separately from
// disk/muxer errors, for which committing the file would be unsafe.
export type RecordingResult = {
  reason?: 'stream-error' | 'buffer-limit' | 'audio-error' | 'source-changed';
};
