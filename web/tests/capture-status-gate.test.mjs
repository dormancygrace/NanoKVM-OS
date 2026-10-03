import assert from 'node:assert/strict';
import test from 'node:test';
import { createCaptureStatusGate } from '../src/pages/desktop/capture-status/status-gate.ts';

const status = result => ({ ok: result >= 0, result, mode: 'direct', message: '', severity: result < 0 ? 'error' : '', updatedAt: '' });
function harness() {
  const output = [];
  const timers = new Map();
  let id = 0;
  const gate = createCaptureStatusGate(s => output.push(s), (cb, delay) => {
    assert.equal(delay, 500);
    timers.set(++id, cb);
    return id;
  }, token => timers.delete(token));
  const fire = () => { const callbacks = [...timers.values()]; timers.clear(); callbacks.forEach(cb => cb()); };
  return { gate, output, timers, fire };
}
test('a recovered single-frame encode error never flashes', () => {
  const h = harness();
  h.gate.accept(status(-2));
  assert.equal(h.output.length, 0);
  h.gate.accept(status(0));
  h.fire();
  assert.deepEqual(h.output, [null]);
});
test('persistent or silent failure becomes visible without another event', () => {
  const h = harness();
  h.gate.accept(status(-2));
  h.gate.accept(status(-3));
  assert.equal(h.timers.size, 1);
  h.fire();
  assert.equal(h.output.at(-1).result, -3);
  h.gate.accept(status(0));
  assert.equal(h.output.at(-1), null);
});
test('HDMI errors and mode-change warnings remain immediate', () => {
  const h = harness();
  h.gate.accept(status(-2));
  h.gate.accept(status(-7));
  assert.equal(h.output.at(-1).result, -7);
  assert.equal(h.timers.size, 0);
  h.gate.accept(status(-4));
  assert.equal(h.output.at(-1).result, -4);
});
test('changing transport or unmounting cancels pending notification', () => {
  const h = harness();
  h.gate.accept(status(-2));
  h.gate.dispose();
  h.fire();
  assert.deepEqual(h.output, []);
});
