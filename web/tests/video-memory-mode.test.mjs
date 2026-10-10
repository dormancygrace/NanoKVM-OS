import assert from 'node:assert/strict';
import test from 'node:test';

import {
  canChooseFixed,
  composeVideoMemoryMode,
  parseVideoMemoryMode,
  pickVideoMemoryMode,
  videoMemoryResolutions
} from '../src/lib/video-memory-mode.ts';

const all = ['fhd', 'fhd-fixed', 'qhd', 'qhd-fixed', 'uhd'];

test('a resolution and the Fixed flag compose the mode string', () => {
  assert.equal(composeVideoMemoryMode('fhd', false), 'fhd');
  assert.equal(composeVideoMemoryMode('fhd', true), 'fhd-fixed');
  assert.equal(composeVideoMemoryMode('qhd', false), 'qhd');
  assert.equal(composeVideoMemoryMode('qhd', true), 'qhd-fixed');
});

test('UHD is always fixed, whatever the flag says', () => {
  assert.equal(composeVideoMemoryMode('uhd', false), 'uhd');
  assert.equal(composeVideoMemoryMode('uhd', true), 'uhd');
});

test('every mode parses back to what composes it', () => {
  for (const mode of all) {
    const parsed = parseVideoMemoryMode(mode);
    assert.ok(parsed, mode);
    assert.equal(composeVideoMemoryMode(parsed.resolution, parsed.fixed), mode);
  }
  assert.deepEqual(parseVideoMemoryMode('uhd'), { resolution: 'uhd', fixed: true });
  assert.deepEqual(videoMemoryResolutions, ['fhd', 'qhd', 'uhd']);
});

test('names that are not modes do not parse', () => {
  // The old names are mapped by the server and never reach the interface.
  for (const mode of ['', 'cma', 'fixed', 'uhd-fixed', 'unknown', 'FHD']) {
    assert.equal(parseVideoMemoryMode(mode), undefined, mode);
  }
});

test('the wish is met by an installed image, else its other variant', () => {
  assert.equal(pickVideoMemoryMode(all, 'qhd', true), 'qhd-fixed');
  assert.equal(pickVideoMemoryMode(all, 'uhd', false), 'uhd');
  assert.equal(pickVideoMemoryMode(['fhd', 'qhd'], 'qhd', true), 'qhd');
  assert.equal(pickVideoMemoryMode(['fhd', 'qhd-fixed'], 'qhd', false), 'qhd-fixed');
  assert.equal(pickVideoMemoryMode(['fhd'], 'uhd', true), undefined);
  assert.equal(pickVideoMemoryMode([], 'fhd', false), undefined);
});

test('Fixed can be chosen only where both variants are installed', () => {
  assert.equal(canChooseFixed(all, 'fhd'), true);
  assert.equal(canChooseFixed(all, 'qhd'), true);
  assert.equal(canChooseFixed(all, 'uhd'), false);
  assert.equal(canChooseFixed(['fhd', 'qhd', 'uhd'], 'qhd'), false);
  assert.equal(canChooseFixed(['fhd-fixed'], 'fhd'), false);
});
