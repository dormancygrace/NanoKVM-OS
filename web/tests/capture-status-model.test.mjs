import assert from 'node:assert/strict';
import test from 'node:test';

import {
  getCaptureStatusMessageKey,
  getVideoMemoryNeed,
  parseCaptureStatusMessage
} from '../src/pages/desktop/capture-status/model.ts';

const wrap = (status) => ({ data: JSON.stringify({ data: JSON.stringify(status) }) });

test('an input refused for the video memory names its size', () => {
  const status = parseCaptureStatusMessage(
    wrap({ ok: false, result: -6, inputWidth: 3840, inputHeight: 2160, videoMemory: true })
  );
  assert.deepEqual(getVideoMemoryNeed(status), { size: '3840 × 2160' });
});

test('other unsupported inputs keep the generic message', () => {
  for (const status of [
    { ok: false, result: -6 },
    { ok: false, result: -6, inputWidth: 3840, inputHeight: 2160, videoMemory: false },
    // Only the boolean flag counts, not a string.
    { ok: false, result: -6, inputWidth: 3840, inputHeight: 2160, videoMemory: 'true' },
    { ok: false, result: -6, inputWidth: 0, inputHeight: 2160, videoMemory: true },
    { ok: false, result: -1, inputWidth: 3840, inputHeight: 2160, videoMemory: true }
  ]) {
    assert.equal(getVideoMemoryNeed(parseCaptureStatusMessage(wrap(status))), null);
  }
  assert.equal(getCaptureStatusMessageKey(-6), 'screen.captureStatus.unsupportedResolution');
});
