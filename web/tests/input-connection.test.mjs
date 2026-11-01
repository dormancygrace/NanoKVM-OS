import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

// page: a fake document whose visibility the test changes (none: always visible).
function fixture({ page } = {}) {
  let now = 1000;
  const timers = new Map();
  let id = 0;
  const sockets = [];
  let expired = 0;
  class Socket {
    static CONNECTING = 0;
    static OPEN = 1;
    readyState = 0;
    sent = [];
    constructor() {
      sockets.push(this);
    }
    send(data) {
      this.sent.push(data);
    }
    close() {
      this.readyState = 3;
      this.onclose?.({ code: 1000 });
    }
    open() {
      this.readyState = 1;
      this.onopen();
    }
    reply() {
      this.onmessage({ data: JSON.stringify({ type: 'heartbeat', data: '' }) });
    }
  }
  const source = readFileSync(new URL('../src/lib/websocket.ts', import.meta.url), 'utf8').replace(
    /^import .*;\n/gm,
    ''
  );
  const code = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 }
  }).outputText;
  const sandbox = {
    exports: {},
    WebSocket: Socket,
    getBaseUrl: () => 'ws://test',
    notifyAuthExpired: () => expired++,
    console: { log() {}, error() {} },
    Uint8Array,
    ArrayBuffer,
    Date: { now: () => now },
    setInterval: (fn) => {
      timers.set(++id, { fn, interval: true });
      return id;
    },
    clearInterval: (i) => timers.delete(i),
    setTimeout: (fn, ms) => {
      timers.set(++id, { fn, interval: false, ms });
      return id;
    },
    clearTimeout: (i) => timers.delete(i)
  };
  if (page) sandbox.document = page;
  const holdSource = readFileSync(new URL('../src/lib/hold-keepalive.ts', import.meta.url), 'utf8');
  const holdCode = ts.transpileModule(holdSource, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 }
  }).outputText;
  vm.runInNewContext(holdCode, sandbox);
  sandbox.HoldKeepalive = sandbox.exports.HoldKeepalive;
  sandbox.KEEPALIVE_MESSAGE = sandbox.exports.KEEPALIVE_MESSAGE;
  vm.runInNewContext(code, sandbox);
  return {
    client: new sandbox.exports.WsClient({ heartbeatInterval: 100 }),
    sockets,
    tick(ms = 100) {
      now += ms;
      for (const [i, t] of [...timers]) {
        if (!timers.has(i)) continue;
        if (!t.interval) timers.delete(i);
        t.fn();
      }
    },
    timers,
    // Delays of the pending one-shot timers.
    timeouts: () => [...timers.values()].filter((t) => !t.interval).map((t) => t.ms),
    expired: () => expired
  };
}

function fakePage() {
  const listeners = new Set();
  return {
    visibilityState: 'visible',
    addEventListener: (type, listener) => type === 'visibilitychange' && listeners.add(listener),
    show(state) {
      this.visibilityState = state;
      listeners.forEach((listener) => listener());
    }
  };
}

test('retry severity, recovery, stale socket callbacks and explicit close', () => {
  const f = fixture(),
    c = f.client;
  let changes = 0;
  const unsubscribe = c.subscribeConnectionStatus(() => changes++);
  c.connect();
  assert.equal(c.getConnectionStatus(), 'connecting');
  const old = f.sockets[0];
  old.open();
  assert.equal(c.getConnectionStatus(), 'connected');
  old.close();
  assert.equal(c.getConnectionStatus(), 'reconnecting');
  f.tick();
  f.sockets.at(-1).close();
  f.tick();
  f.sockets.at(-1).close();
  assert.equal(c.getConnectionStatus(), 'disconnected');
  f.tick();
  f.sockets.at(-1).open();
  assert.equal(c.getConnectionStatus(), 'connected');
  old.onclose({ code: 4401 });
  assert.equal(c.getConnectionStatus(), 'connected');
  assert.equal(f.expired(), 0);
  c.close();
  assert.equal(c.getConnectionStatus(), 'idle');
  assert.equal(f.timers.size, 0);
  assert.ok(changes >= 5);
  unsubscribe();
});

test('an expired session ends the socket for good and signals the login page', () => {
  const f = fixture(),
    c = f.client;
  c.connect();
  f.sockets[0].open();
  f.sockets[0].onclose({ code: 4401 });
  assert.equal(f.expired(), 1);
  assert.equal(c.getConnectionStatus(), 'disconnected');
  f.tick();
  f.tick(5000);
  assert.equal(f.sockets.length, 1, 'no reconnect attempt');
  assert.equal(f.timers.size, 0);
});
test('acknowledged heartbeat detects a half-open connection; old server remains compatible', () => {
  const f = fixture(),
    c = f.client;
  c.connect();
  f.sockets[0].open();
  f.tick(400);
  assert.equal(c.getConnectionStatus(), 'connected');
  f.sockets[0].reply();
  // Three regular heartbeat windows, rather than one delayed timer callback.
  f.tick(100);
  f.tick(100);
  f.tick(100);
  assert.equal(c.getConnectionStatus(), 'reconnecting');
  f.tick();
  assert.equal(f.sockets.length, 2);
  f.sockets[1].open();
  f.sockets[1].reply();
  f.tick(100);
  f.sockets[1].reply();
  f.tick(100);
  assert.equal(c.getConnectionStatus(), 'connected');
  c.close();
});

test('paused browser timers probe a healthy socket without reconnecting on return', () => {
  const f = fixture(),
    c = f.client;
  c.connect();
  const socket = f.sockets[0];
  socket.open();
  socket.reply();
  const sent = socket.sent.length;
  // The page resumes after 30 intervals; only one interval callback runs.
  // The open socket can still answer as soon as the page services events.
  f.tick(3000);
  assert.equal(c.getConnectionStatus(), 'connected');
  assert.equal(f.sockets.length, 1);
  assert.equal(socket.readyState, 1);
  assert.equal(socket.sent.length, sent + 1);
  assert.deepEqual(Array.from(socket.sent.at(-1)), [0]);
  socket.reply();
  f.tick(100);
  socket.reply();
  assert.equal(c.getConnectionStatus(), 'connected');
  assert.equal(f.sockets.length, 1);
  c.close();
  assert.equal(f.timers.size, 0);
});

test('a socket the browser cut in the background is replaced at once on return', () => {
  const page = fakePage();
  const f = fixture({ page }),
    c = f.client;
  c.connect();
  f.sockets[0].open();
  page.show('hidden');
  // Lost while hidden: the backoff stays.
  f.sockets[0].onclose({ code: 1006 });
  assert.deepEqual(f.timeouts(), [3000]);
  // On return the pending retry goes now, without waiting for a timer.
  page.show('visible');
  assert.equal(f.sockets.length, 2);
  assert.deepEqual(f.timeouts(), []);
  f.sockets[1].open();
  assert.equal(c.getConnectionStatus(), 'connected');
  c.close();
});

test('a close reported on return retries at once, also after background attempts', () => {
  const page = fakePage();
  const f = fixture({ page }),
    c = f.client;
  c.connect();
  f.sockets[0].open();
  page.show('hidden');
  f.sockets[0].onclose({ code: 1006 });
  f.tick();
  // A background socket whose open never reached the page, and that the
  // browser reports closed only once the tab is back.
  assert.equal(f.sockets.length, 2);
  page.show('visible');
  assert.deepEqual(f.timeouts(), []);
  f.sockets[1].onclose({ code: 1006 });
  assert.deepEqual(f.timeouts(), [0]);
  assert.equal(c.getConnectionStatus(), 'reconnecting');
  f.tick();
  f.sockets[2].open();
  assert.equal(c.getConnectionStatus(), 'connected');
  // A visible page that keeps failing still backs off after its first retry.
  f.sockets[2].onclose({ code: 1006 });
  assert.deepEqual(f.timeouts(), [0]);
  f.tick();
  f.sockets[3].onclose({ code: 1006 });
  assert.deepEqual(f.timeouts(), [3000]);
  c.close();
});
