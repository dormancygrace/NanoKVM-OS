import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import i18next from 'i18next';
import ts from 'typescript';

function fixture() {
  let now = 1000;
  const timers = new Map();
  let id = 0;
  const sockets = [];
  let expired = 0;
  class Socket {
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
    setTimeout: (fn) => {
      timers.set(++id, { fn, interval: false });
      return id;
    },
    clearTimeout: (i) => timers.delete(i)
  };
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
    expired: () => expired
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

test('acknowledged heartbeat detects a half-open connection; old server remains compatible', () => {
  const f = fixture(),
    c = f.client;
  c.connect();
  f.sockets[0].open();
  f.tick(400);
  assert.equal(c.getConnectionStatus(), 'connected');
  f.sockets[0].reply();
  f.tick(300);
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

test('intentional close while connecting cannot reconnect or report a late open', () => {
  const f = fixture();
  f.client.connect();
  const socket = f.sockets[0];
  f.client.close();
  socket.open();
  assert.equal(f.client.getConnectionStatus(), 'idle');
  assert.equal(f.timers.size, 0);
});

test('connection feedback resolves in both shipped locales and fallback', async () => {
  const resources = {};
  for (const language of ['en', 'ru']) {
    const translation = JSON.parse(
      readFileSync(new URL(`../src/i18n/locales/${language}.json`, import.meta.url), 'utf8')
    );
    resources[language] = { translation };
  }
  const translator = i18next.createInstance();
  await translator.init({ resources, lng: 'en', fallbackLng: 'en' });
  for (const language of ['en', 'ru', 'de']) {
    await translator.changeLanguage(language);
    for (const status of ['idle', 'connecting', 'connected', 'reconnecting', 'disconnected']) {
      const key = `inputConnection.${status}`;
      assert.notEqual(translator.t(key), key);
      assert.ok(translator.t(key).length > 5);
    }
  }
});

test('socket opts in once, sends hold keepalive only while held, and resets on disconnect', () => {
  const f = fixture(),
    c = f.client;
  c.connect();
  const socket = f.sockets[0];
  socket.open();
  assert.deepEqual(
    socket.sent.map((x) => Array.from(x)),
    [[4], [0]]
  );
  c.setInputEnabled(true);
  c.setControlEnabled(true);
  c.send(new Uint8Array([1, 0, 0, 4, 0, 0, 0, 0, 0]));
  assert.equal(f.timers.size, 2);
  f.tick(50);
  assert.ok(socket.sent.some((x, i) => i > 1 && x.length === 1 && x[0] === 4));
  c.setControlEnabled(false);
  assert.equal(f.timers.size, 1);
  assert.deepEqual(Array.from(socket.sent.at(-1)), [1, 0, 0, 0, 0, 0, 0, 0, 0]);
  c.setControlEnabled(true);
  c.send(new Uint8Array([2, 1, 0, 0, 0]));
  assert.equal(f.timers.size, 2);
  socket.close();
  assert.equal(f.timers.size, 1); // reconnect only
  c.close();
  assert.equal(f.timers.size, 0);
});
