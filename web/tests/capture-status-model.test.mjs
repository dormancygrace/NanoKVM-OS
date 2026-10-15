import assert from 'node:assert/strict';
import test from 'node:test';

import {
  getCaptureStatusMessageKey,
  getVideoMemoryNeed,
  parseCaptureStatusMessage
} from '../src/pages/desktop/capture-status/model.ts';

test('a refused MJPEG capture has its own message', () => {
  assert.equal(getCaptureStatusMessageKey(-8), 'screen.captureStatus.mjpeg4k');
  assert.equal(getCaptureStatusMessageKey(-9), 'screen.captureStatus.unavailable');
});

const wrap = (status) => ({ data: JSON.stringify({ data: JSON.stringify(status) }) });

test('an input refused for the video memory names its size and the mode it needs', () => {
  const status = parseCaptureStatusMessage(
    wrap({ ok: false, result: -6, inputWidth: 2560, inputHeight: 1440, videoMemory: 'qhd' })
  );
  assert.deepEqual(getVideoMemoryNeed(status), { size: '2560 × 1440', mode: 'qhd' });
});

test('other unsupported inputs keep the generic message', () => {
  for (const status of [
    { ok: false, result: -6 },
    { ok: false, result: -6, inputWidth: 2560, inputHeight: 1440, videoMemory: 'fhd' },
    { ok: false, result: -6, inputWidth: 0, inputHeight: 1440, videoMemory: 'qhd' },
    { ok: false, result: -1, inputWidth: 2560, inputHeight: 1440, videoMemory: 'qhd' }
  ]) {
    assert.equal(getVideoMemoryNeed(parseCaptureStatusMessage(wrap(status))), null);
  }
  assert.equal(getCaptureStatusMessageKey(-6), 'screen.captureStatus.unsupportedResolution');
});
