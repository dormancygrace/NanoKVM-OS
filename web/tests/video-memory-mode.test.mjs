import assert from 'node:assert/strict';
import test from 'node:test';

import {
  parseVideoMemoryMode,
  pickVideoMemoryMode,
  videoMemoryResolutions
} from '../src/lib/video-memory-mode.ts';

test('every mode is a resolution', () => {
  assert.deepEqual(videoMemoryResolutions, ['fhd', 'qhd', 'uhd']);
  for (const mode of videoMemoryResolutions) assert.equal(parseVideoMemoryMode(mode), mode);
});

test('names that are not modes do not parse', () => {
  // The old names are mapped by the server and never reach the interface.
  for (const mode of [
    '',
    'cma',
    'fixed',
    'fhd-fixed',
    'qhd-fixed',
    'uhd-fixed',
    'unknown',
    'FHD'
  ]) {
    assert.equal(parseVideoMemoryMode(mode), undefined, mode);
  }
});

test('a resolution is offered only with its installed image', () => {
  assert.equal(pickVideoMemoryMode(['fhd', 'qhd', 'uhd'], 'qhd'), 'qhd');
  assert.equal(pickVideoMemoryMode(['fhd'], 'uhd'), undefined);
  assert.equal(pickVideoMemoryMode([], 'fhd'), undefined);
});
