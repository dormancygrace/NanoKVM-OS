import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';

import { reconnectDelay, ReconnectGate } from '../src/lib/reconnect-delay.ts';

const middle = () => 0.5;

test('no failure, no wait', () => {
  assert.equal(reconnectDelay(0, middle), 0);
});

test('the wait doubles from 2 s to a minute', () => {
  assert.deepEqual(
    [1, 2, 3, 4, 5, 6, 7, 20].map((n) => reconnectDelay(n, middle)),
    [2000, 4000, 8000, 16000, 32000, 60000, 60000, 60000]
  );
});

test('jitter stays within a fifth either way', () => {
  assert.equal(
    reconnectDelay(3, () => 0),
    6400
  );
  assert.equal(
    reconnectDelay(3, () => 0.999999),
    9600
  );
});

test('the gate holds automatic starts back after failures', () => {
  const gate = new ReconnectGate();
  assert.ok(gate.allows(0));
  gate.failed(1000, middle);
  assert.ok(!gate.allows(2999));
  assert.ok(gate.allows(3000));
  for (let i = 0; i < 4; i++) gate.failed(10000, middle);
  assert.ok(!gate.allows(10000 + 31999));
  assert.ok(gate.allows(10000 + 32000));
  gate.connected();
  assert.ok(gate.allows(10000));
});

test('the USB audio hook goes through the gate', () => {
  const hook = fs.readFileSync(new URL('../src/hooks/useUsbAudio.ts', import.meta.url), 'utf8');
  const retry = hook.slice(
    hook.indexOf('const attempt = () =>'),
    hook.indexOf('async function start(')
  );
  assert.match(retry, /gate\.current\.allows\(performance\.now\(\)\)/);
  const fail = hook.slice(hook.indexOf('const fail = ('), hook.indexOf('const resumed ='));
  assert.match(fail, /gate\.current\.failed\(/);
  // Recovered audio on both paths, a click on the switch and the composition.
  assert.ok((hook.match(/gate\.current\.connected\(\)/g) ?? []).length >= 4);
});
