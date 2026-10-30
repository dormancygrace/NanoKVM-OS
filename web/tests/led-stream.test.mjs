import assert from 'node:assert/strict';
import test from 'node:test';

import { createLEDStream } from '../src/lib/led-stream.ts';

function fixture({ visible = true, initial = null, failConnect = false } = {}) {
  let now = 0;
  const timers = new Set();
  const visibility = new Set();
  const fallbacks = new Set();
  const sources = [];
  const env = {
    now: () => now,
    isVisible: () => visible,
    onVisibilityChange: (fn) => {
      visibility.add(fn);
      return () => visibility.delete(fn);
    },
    later: (fn, ms) => {
      const timer = { fn, due: now + ms };
      timers.add(timer);
      return () => timers.delete(timer);
    }
  };
  const stream = createLEDStream(
    () => {
      if (failConnect) throw new Error('unavailable');
      const source = {
        onmessage: null,
        onerror: null,
        closes: 0,
        close() {
          this.closes++;
        }
      };
      sources.push(source);
      return source;
    },
    (listener) => {
      fallbacks.add(listener);
      listener(initial, -1); // the shared poll may synchronously replay its cache
      return () => fallbacks.delete(listener);
    },
    env
  );
  return {
    stream,
    sources,
    timers,
    visibility,
    fallbacks,
    send: (status, source = sources.at(-1)) => source.onmessage({ data: JSON.stringify(status) }),
    show(value) {
      visible = value;
      for (const fn of visibility) fn();
    },
    advance(ms) {
      const end = now + ms;
      while (true) {
        const next = [...timers].filter((x) => x.due <= end).sort((a, b) => a.due - b.due)[0];
        if (!next) break;
        timers.delete(next);
        now = next.due;
        next.fn();
      }
      now = end;
    }
  };
}
const off = { pwr: true, hdd: false };
const on = { pwr: true, hdd: true };

test('one stream is shared; a successful snapshot replaces fallback polling', () => {
  const f = fixture({ initial: off });
  const a = [],
    b = [];
  const stopA = f.stream.subscribe((s) => a.push(s));
  const stopB = f.stream.subscribe((s) => b.push(s));
  assert.equal(f.sources.length, 1);
  assert.equal(f.fallbacks.size, 1);
  assert.deepEqual(b.at(-1), off);
  f.send(on);
  assert.deepEqual(a.at(-1), on);
  assert.deepEqual(b.at(-1), on);
  assert.equal(f.fallbacks.size, 0);
  stopA();
  assert.equal(f.sources[0].closes, 0);
  stopB();
  assert.equal(f.sources[0].closes, 1);
  assert.equal(f.visibility.size, 0);
  assert.equal(f.timers.size, 0);
});

test('an on/off pair delivered together remains visible for 40 ms without pulsing', () => {
  const f = fixture();
  const states = [];
  const stop = f.stream.subscribe((s) => states.push(s));
  f.send(on);
  f.send(off);
  assert.equal(states.at(-1).hdd, true);
  f.advance(39);
  assert.equal(states.at(-1).hdd, true);
  f.advance(1);
  assert.deepEqual(states.at(-1), off);
  const count = states.length;
  f.advance(60000);
  assert.equal(states.length, count);
  assert.equal(f.timers.size, 0);
  stop();
});

test('new activity cancels a pending off; unavailable data clears immediately', () => {
  const f = fixture();
  const states = [];
  const stop = f.stream.subscribe((s) => states.push(s));
  f.send(on);
  f.send(off);
  f.advance(30);
  f.send(on);
  f.advance(20);
  assert.deepEqual(states.at(-1), on);
  f.send(off);
  f.advance(19);
  assert.equal(states.at(-1).hdd, true);
  f.send(null);
  assert.equal(states.at(-1), null);
  f.advance(100);
  assert.equal(states.at(-1), null);
  stop();
});

test('hidden tabs close connections; late events and fallback callbacks are ignored', () => {
  const f = fixture();
  const seen = [];
  const stop = f.stream.subscribe((s) => seen.push(s));
  const oldSource = f.sources[0],
    oldFallback = [...f.fallbacks][0];
  f.send(on);
  f.send(off);
  f.show(false);
  assert.equal(f.timers.size, 0);
  assert.equal(oldSource.closes, 1);
  assert.deepEqual(seen.at(-1), off);
  f.send(on, oldSource);
  oldFallback(on);
  assert.deepEqual(seen.at(-1), off);
  f.show(true);
  assert.equal(f.sources.length, 2);
  f.send(on, oldSource);
  oldFallback(on);
  assert.deepEqual(seen.at(-1), off);
  f.send(off);
  assert.deepEqual(seen.at(-1), off);
  stop();
});

test('errors retain power, clear stale activity, resume fallback and reconnect once after a delay', () => {
  const f = fixture();
  const seen = [];
  const stop = f.stream.subscribe((s) => seen.push(s));
  f.send(on);
  const old = f.sources[0];
  old.onerror();
  assert.equal(old.closes, 1);
  assert.deepEqual(seen.at(-1), off);
  assert.equal(f.fallbacks.size, 1);
  const late = [...f.fallbacks][0];
  late(off);
  assert.deepEqual(seen.at(-1), off);
  old.onerror();
  assert.equal(f.timers.size, 1);
  f.advance(2999);
  assert.equal(f.sources.length, 1);
  f.advance(1);
  assert.equal(f.sources.length, 2);
  f.send(on);
  late(off);
  assert.deepEqual(seen.at(-1), on);
  assert.equal(f.fallbacks.size, 0);
  stop();
  f.send(on);
  assert.equal(f.timers.size, 0);
});

test('an initially hidden tab opens nothing; invalid data and missing EventSource use fallback', () => {
  const f = fixture({ visible: false });
  const stop = f.stream.subscribe(() => {});
  assert.equal(f.sources.length, 0);
  assert.equal(f.fallbacks.size, 0);
  f.show(true);
  f.send({ pwr: 'true', hdd: false });
  assert.equal(f.sources[0].closes, 1);
  assert.equal(f.fallbacks.size, 1);
  stop();
  const missing = fixture({ failConnect: true });
  const stopMissing = missing.stream.subscribe(() => {});
  assert.equal(missing.fallbacks.size, 1);
  assert.equal(missing.timers.size, 1);
  stopMissing();
  assert.equal(missing.timers.size, 0);
  assert.equal(missing.fallbacks.size, 0);
});

test('reconnect keeps confirmed power across missing fallback and accepts real state changes', () => {
  const f = fixture();
  const seen = [];
  const stop = f.stream.subscribe((s) => seen.push(s));
  f.send(off);
  const start = seen.length;
  f.sources[0].onerror();
  for (const callback of f.fallbacks) callback(null);
  assert.equal(seen.at(-1)?.pwr, true);
  f.advance(3000);
  assert.equal(seen.at(-1)?.pwr, true);
  assert.ok(seen.slice(start).every((s) => s?.pwr === true));
  f.send({ pwr: false, hdd: false });
  assert.equal(seen.at(-1).pwr, false);
  f.send(off);
  f.send(null);
  assert.equal(seen.at(-1), null);
  stop();
});

test('cached and in-flight fallback polls cannot undo a newer stream snapshot', () => {
  const f = fixture({ initial: { pwr: false, hdd: false } });
  const seen = [];
  const stop = f.stream.subscribe((s) => seen.push(s));
  f.advance(10);
  f.send(off);
  f.sources[0].onerror();
  assert.deepEqual(seen.at(-1), off);
  const poll = [...f.fallbacks][0];
  poll({ pwr: false, hdd: false }, 5);
  assert.deepEqual(seen.at(-1), off);
  poll({ pwr: false, hdd: false }, 11);
  assert.equal(seen.at(-1).pwr, false);
  stop();
  const next = [];
  const stopNext = f.stream.subscribe((s) => next.push(s));
  assert.equal(next[0], null);
  stopNext();
});
