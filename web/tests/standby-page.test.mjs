import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';

const source = readFileSync(new URL('../../server/standby/page.js', import.meta.url), 'utf8');
async function page(result, failure, fetchOverride) {
  const nodes = new Map();
  const timers = [];
  const context = {
    document: {
      querySelector: (id) => {
        if (!nodes.has(id)) nodes.set(id, { value: '', disabled: true, textContent: '' });
        return nodes.get(id);
      }
    },
    sessionStorage: { getItem: () => 'test-key', setItem: () => {} },
    navigator: { clipboard: { writeText: async () => {} } },
    fetch:
      fetchOverride ||
      (async () => {
        if (failure) throw new Error(failure);
        return { ok: true, json: async () => result };
      }),
    AbortSignal: { timeout: () => undefined },
    setTimeout: (f) => timers.push(f),
    confirm: () => true,
    location: { assign: () => {} }
  };
  vm.runInNewContext(source, context);
  await new Promise((resolve) => setImmediate(resolve));
  return { nodes, timers };
}
test('RAM page announces safe only after a positive ready result', async () => {
  for (const result of [
    { state: 'preparing', safe: false },
    { state: 'preparing', safe: true },
    { state: 'ready', safe: false },
    { state: 'failed', safe: false, error: 'unmount busy' }
  ]) {
    const { nodes } = await page(result);
    assert.doesNotMatch(nodes.get('#state').textContent, /You can disconnect/);
    assert.equal(nodes.get('#start').disabled, result.state !== 'failed');
  }
  const { nodes } = await page({ state: 'ready', safe: true });
  assert.match(nodes.get('#state').textContent, /You can disconnect/);
  assert.equal(nodes.get('#start').disabled, false);
});
test('lost status immediately removes the safe message and disables restart', async () => {
  const { nodes, timers } = await page(undefined, 'connection lost');
  assert.match(nodes.get('#state').textContent, /Keep power connected/);
  assert.equal(nodes.get('#start').disabled, true);
  assert.equal(timers.length, 1);
});
test('magic-packet status is distinct from authenticated web restart', async () => {
  const { nodes } = await page({
    state: 'ready',
    safe: true,
    magicWake: true,
    mac: '02:11:22:33:44:55'
  });
  assert.match(nodes.get('#wake').textContent, /02:11:22:33:44:55/);
  assert.match(nodes.get('#wake').textContent, /no password/);
});

test('a status response in flight cannot restore safe after restart was requested', async () => {
  let release;
  let count = 0;
  const { nodes, timers } = await page(null, null, async (url) => {
    if (url === '/standby/start') return { ok: true };
    count++;
    if (count === 1) return { ok: true, json: async () => ({ state: 'ready', safe: true }) };
    return {
      ok: true,
      json: () =>
        new Promise((resolve) => {
          release = resolve;
        })
    };
  });
  const polling = timers.shift()();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(typeof release, 'function');
  await nodes.get('#start').onclick();
  release({ state: 'ready', safe: true });
  await polling;
  assert.doesNotMatch(nodes.get('#state').textContent, /You can disconnect/);
  assert.match(nodes.get('#state').textContent, /starting. Keep power connected/);
  assert.equal(nodes.get('#start').disabled, true);
});
