import assert from 'node:assert/strict';
import test from 'node:test';

import { captureMediaElement, screenshotFilename } from '../src/lib/screenshot.ts';

class FakeVideo {
  videoWidth = 1440;
  videoHeight = 2560;
  readyState = 2;
}

globalThis.HTMLVideoElement = FakeVideo;
globalThis.HTMLMediaElement = { HAVE_CURRENT_DATA: 2 };

test('captures the source pixels at their native portrait dimensions as PNG', async () => {
  let draw;
  globalThis.document = {
    createElement: () => ({
      width: 0,
      height: 0,
      getContext() {
        return {
          drawImage: (...args) => {
            draw = args;
          }
        };
      },
      toBlob(callback, type) {
        assert.equal(this.width, 1440);
        assert.equal(this.height, 2560);
        assert.equal(type, 'image/png');
        callback(new Blob(['png'], { type }));
      }
    })
  };

  const video = new FakeVideo();
  const blob = await captureMediaElement(video);
  assert.equal(blob.type, 'image/png');
  assert.deepEqual(draw, [video, 0, 0, 1440, 2560]);
});

test('rejects video without a decoded frame', async () => {
  const video = new FakeVideo();
  video.readyState = 1;
  await assert.rejects(captureMediaElement(video), /screenshot-no-video/);
});

test('uses a local timestamp and milliseconds in repeat-safe filenames', () => {
  const date = new Date(2026, 8, 14, 9, 7, 5, 42);
  assert.equal(screenshotFilename(date), 'NanoKVM-screenshot-2026-09-14_09-07-05-042.png');
});
