import test from 'node:test';
import assert from 'node:assert/strict';

import { validIceAddress } from '../src/lib/ice-address.ts';

test('STUN and TURN addresses are host:port, as the server checks them', () => {
  for (const ok of ['stun.l.google.com:19302', 'stun:a.example:3478', 'turn:10.0.0.1:3478', '[2001:db8::1]:3478', ' b.example:1 ']) {
    assert.equal(validIceAddress(ok), true, ok);
  }
  for (const bad of ['a.example', 'a.example:0', 'a.example:70000', 'a b:3478', 'https://a.example:3478', '']) {
    assert.equal(validIceAddress(bad), false, bad);
  }
});
