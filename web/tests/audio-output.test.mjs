import assert from 'node:assert/strict';
import test from 'node:test';

import { createAudioOutput } from '../src/lib/audio-output.ts';

class Context extends EventTarget {
  state = 'suspended';
  allowed = false;
  calls = 0;
  waiters = [];
  destination = {};
  createGain() {
    return { connect() {}, disconnect() {} };
  }
  resume() {
    this.calls++;
    if (this.allowed) this.run();
    if (this.state === 'running') return Promise.resolve();
    return new Promise((resolve) => this.waiters.push(resolve));
  }
  run() {
    this.state = 'running';
    this.dispatchEvent(new Event('statechange'));
    this.waiters.splice(0).forEach((resolve) => resolve());
  }
  close() {
    this.state = 'closed';
    this.dispatchEvent(new Event('statechange'));
    this.waiters.splice(0).forEach((resolve) => resolve());
    return Promise.resolve();
  }
}
function setup(t) {
  const old = ['window', 'document', 'AudioContext'].map((key) => [key, globalThis[key]]);
  globalThis.window = new EventTarget();
  globalThis.document = new EventTarget();
  document.visibilityState = 'visible';
  globalThis.AudioContext = Context;
  t.after(() => {
    output.close();
    old.forEach(([key, value]) => {
      if (value === undefined) delete globalThis[key];
      else globalThis[key] = value;
    });
  });
  const blocked = [];
  const output = createAudioOutput((value) => blocked.push(value));

  return { output, blocked };
}

for (const gesture of ['pointerdown', 'pointerup', 'click', 'keydown']) {
  test('saved listening resumes on ordinary ' + gesture + ' after autoplay waits', async (t) => {
    const { output, blocked } = setup(t);
    let ready = false;
    const pending = output.resume().then(() => {
      ready = true;
    });
    await Promise.resolve();
    assert.equal(ready, false);
    assert.equal(blocked.at(-1), true);
    output.context.allowed = true;
    window.dispatchEvent(new Event(gesture));
    // resume must happen inside the event, before a microtask loses activation.
    assert.equal(output.context.state, 'running');
    await pending;
    assert.equal(blocked.at(-1), false);
  });
}
test('an interrupted authorized context resumes without a new gesture', async (t) => {
  const { output, blocked } = setup(t);
  output.context.allowed = true;
  await output.resume();
  output.context.state = 'suspended';
  output.context.dispatchEvent(new Event('statechange'));
  assert.equal(output.context.state, 'running');
  assert.equal(blocked.at(-1), false);
});
test('returning to a visible page retries a suspended context', async (t) => {
  const { output } = setup(t);
  void output.resume();
  output.context.allowed = true;
  document.dispatchEvent(new Event('visibilitychange'));
  assert.equal(output.context.state, 'running');
});
test('stop removes recovery listeners and ignores delayed resume completions', async (t) => {
  const { output, blocked } = setup(t);
  const pending = output.resume();
  output.close();
  const calls = output.context.calls;
  const reports = blocked.length;
  for (const event of ['pointerdown', 'pointerup', 'click', 'keydown', 'focus', 'pageshow']) {
    window.dispatchEvent(new Event(event));
  }
  document.dispatchEvent(new Event('visibilitychange'));
  await pending;
  assert.equal(output.context.calls, calls);
  assert.equal(blocked.length, reports);
  assert.equal(output.context.state, 'closed');
});
test('a failed resume can be retried by the next gesture', async (t) => {
  const { output, blocked } = setup(t);
  const original = output.context.resume;
  output.context.resume = () => Promise.reject(new Error('policy'));
  await assert.rejects(output.resume(), /policy/);
  assert.equal(blocked.at(-1), true);
  output.context.resume = original;
  output.context.allowed = true;
  window.dispatchEvent(new Event('click'));
  assert.equal(output.context.state, 'running');
});
