import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { getKeycode } from '../src/lib/keymap.ts';

// The keyboard descriptor ends its key array at usage 0xE7; a host drops any
// larger code, so a key mapped there would be sent and do nothing.
test('every mapped key fits the keyboard descriptor', () => {
  const source = readFileSync(new URL('../src/lib/keymap.ts', import.meta.url), 'utf8');
  const names = [...source.matchAll(/^\s+(\w+): 0x[0-9a-f]+,?/gim)].map((match) => match[1]);
  assert.ok(names.length > 100);
  for (const name of names) {
    const code = getKeycode(name);
    if (code !== undefined) assert.ok(code <= 0xe7, `${name} = 0x${code.toString(16)}`);
  }
});

test('media keys are not sent as keyboard usages', () => {
  for (const name of ['MediaPlayPause', 'BrowserSearch', 'LaunchApp2', 'Sleep', 'Wake']) {
    assert.equal(getKeycode(name), undefined, name);
  }
  assert.equal(getKeycode('AudioVolumeUp'), 0x80);
  assert.equal(getKeycode('Power'), 0x66);
});
