import assert from 'node:assert/strict';
import test from 'node:test';

import { recordingMimeType, recordStream, withRecordingAudio } from '../src/lib/recording.ts';

class FakeRecorder {
  static last;
  state = 'inactive';
  constructor() {
    FakeRecorder.last = this;
  }
  start() {
    this.state = 'recording';
  }
  chunk(text) {
    this.ondataavailable?.({ data: new Blob([text]) });
  }
  stop() {
    this.state = 'inactive';
    queueMicrotask(() => {
      this.chunk('last');
      this.onstop?.();
    });
  }
}
globalThis.MediaRecorder = FakeRecorder;
function fixture(writer) {
  const track = new EventTarget();
  track.stopped = false;
  track.stop = () => {
    track.stopped = true;
  };
  const stream = { getTracks: () => [track] };
  return { stream, track, writer };
}
test('serializes delayed writes and includes final chunk before closing', async () => {
  const events = [];
  let release;
  const gate = new Promise((ok) => {
    release = ok;
  });
  const f = fixture({
    write: async (blob) => {
      const text = await blob.text();
      events.push('start:' + text);
      if (text === 'first') await gate;
      events.push('end:' + text);
    },
    close: async () => events.push('close'),
    abort: async () => events.push('abort')
  });
  const s = recordStream(f.stream, f.writer, 'video/webm');
  FakeRecorder.last.chunk('first');
  s.stop();
  await new Promise((ok) => setTimeout(ok, 5));
  assert.deepEqual(events, ['start:first']);
  release();
  await s.done;
  assert.deepEqual(events, ['start:first', 'end:first', 'start:last', 'end:last', 'close']);
  assert.equal(f.track.stopped, true);
});
test('write failure aborts the file and releases recording tracks', async () => {
  let aborted = false;
  const f = fixture({
    write: async () => {
      throw new Error('disk full');
    },
    close: async () => assert.fail('must not close failed file'),
    abort: async () => {
      aborted = true;
    }
  });
  const s = recordStream(f.stream, f.writer, 'video/webm');
  const rejection = assert.rejects(s.done, /disk full/);
  FakeRecorder.last.chunk('frame');
  await rejection;
  assert.equal(aborted, true);
  assert.equal(f.track.stopped, true);
});
test('slow storage cannot create an unbounded pending queue', async () => {
  let aborted = false;
  const f = fixture({
    write: async () => {},
    close: async () => assert.fail(),
    abort: async () => {
      aborted = true;
    }
  });
  const s = recordStream(f.stream, f.writer, 'video/webm', 8);
  const rejection = assert.rejects(s.done, /recording-backpressure/);
  FakeRecorder.last.chunk('too much data');
  await rejection;
  assert.equal(aborted, true);
});
test('source ending finishes file and repeated stop is harmless', async () => {
  let closed = 0;
  const f = fixture({
    write: async () => {},
    close: async () => {
      closed++;
    },
    abort: async () => assert.fail()
  });
  const s = recordStream(f.stream, f.writer, 'video/webm');
  f.track.dispatchEvent(new Event('ended'));
  s.stop();
  await s.done;
  assert.equal(closed, 1);
});
test('close failure aborts the unfinished file transaction', async () => {
  let aborted = false;
  const f = fixture({
    write: async () => {},
    close: async () => {
      throw new Error('close failed');
    },
    abort: async () => {
      aborted = true;
    }
  });
  const s = recordStream(f.stream, f.writer, 'video/webm');
  const rejection = assert.rejects(s.done, /close failed/);
  s.stop();
  await rejection;
  assert.equal(aborted, true);
});

test('sound that ends leaves the recording running; the video ends it', async () => {
  const closed = [];
  const video = new EventTarget();
  video.kind = 'video';
  video.stop = () => {};
  const audio = new EventTarget();
  audio.kind = 'audio';
  audio.stopped = false;
  audio.stop = () => {
    audio.stopped = true;
  };
  const writer = { write: async () => {}, close: async () => closed.push('close'), abort: async () => {} };
  const s = recordStream({ getTracks: () => [video, audio] }, writer, 'video/webm');
  audio.dispatchEvent(new Event('ended'));
  await new Promise((ok) => setTimeout(ok, 5));
  assert.equal(FakeRecorder.last.state, 'recording');
  video.dispatchEvent(new Event('ended'));
  await s.done;
  assert.deepEqual(closed, ['close']);
  assert.equal(audio.stopped, true);
});

test('recordings with sound ask for Opus and copy only live audio', () => {
  FakeRecorder.isTypeSupported = (type) => type !== 'video/mp4';
  assert.equal(recordingMimeType(true), 'video/webm;codecs=vp8,opus');
  assert.equal(recordingMimeType(), 'video/webm;codecs=vp8');
  const added = [];
  const stream = { addTrack: (track) => added.push(track) };
  const live = { readyState: 'live', clone: () => 'copy' };
  const ended = { readyState: 'ended', clone: () => assert.fail('ended track copied') };
  withRecordingAudio(stream, { getAudioTracks: () => [live, ended] });
  withRecordingAudio(stream, null);
  assert.deepEqual(added, ['copy']);
});
