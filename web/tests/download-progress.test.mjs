import assert from 'node:assert/strict';
import test from 'node:test';

import { readTransferProgress, transferBytes } from '../src/lib/download-progress.ts';

test('zero, unknown size and legacy percentage remain distinct', () => {
  assert.equal(readTransferProgress({ percentage: '0.00%' }).percent, 0);
  assert.equal(
    readTransferProgress({ downloadedBytes: 20, totalBytes: 0, bytesPerSecond: 10 }).percent,
    null
  );
  assert.equal(readTransferProgress({ percentage: '13.19%' }).percent, 13.19);
  assert.equal(readTransferProgress({ percentage: 'invalid' }).percent, null);
});
test('byte counts win over legacy percentages and invalid speeds are unavailable', () => {
  assert.equal(
    readTransferProgress({ downloadedBytes: 25, totalBytes: 100, percentage: '90%' }).percent,
    25
  );
  assert.equal(readTransferProgress({ downloadedBytes: 101, totalBytes: 100 }).percent, 100);
  for (const value of [NaN, Infinity, -1, '42'])
    assert.equal(readTransferProgress({ bytesPerSecond: value }).speed, null);
  assert.equal(readTransferProgress({ bytesPerSecond: 0 }).speed, 0);
  assert.equal(transferBytes(1024 * 1024), '1.0 MiB');
});
