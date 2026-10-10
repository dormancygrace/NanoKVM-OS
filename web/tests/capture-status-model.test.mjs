import assert from 'node:assert/strict';
import test from 'node:test';
import { getCaptureStatusMessageKey } from '../src/pages/desktop/capture-status/model.ts';

test('a refused MJPEG capture has its own message', () => {
  assert.equal(getCaptureStatusMessageKey(-8), 'screen.captureStatus.mjpeg4k');
  assert.equal(getCaptureStatusMessageKey(-9), 'screen.captureStatus.unavailable');
});
