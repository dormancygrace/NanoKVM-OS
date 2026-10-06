import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

import { swapRequestSize } from '../src/lib/swap-request.ts';

const deferred = () => {
  let resolve, reject;
  const promise = new Promise((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
const settle = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

// Same source-level harness as keyboard-ime: render hooks with dependency comparison,
// stable state setters, cleanup/setup replay and deferred API responses. No device I/O.
function harness(path, exportName, modules = {}, globals = {}) {
  const slots = [];
  let cursor = 0,
    pending = [],
    dirty = false,
    props = {},
    output;
  let translate = (key) => `en:${key}`;
  const equal = (a, b) => a && b && a.length === b.length && a.every((v, i) => Object.is(v, b[i]));
  const react = {
    useState(initial) {
      const i = cursor++;
      if (!slots[i]) {
        const slot = { value: typeof initial === 'function' ? initial() : initial };
        slot.set = (value) => {
          const next = typeof value === 'function' ? value(slot.value) : value;
          if (!Object.is(next, slot.value)) {
            slot.value = next;
            dirty = true;
          }
        };
        slots[i] = slot;
      }
      return [slots[i].value, slots[i].set];
    },
    useRef(initial) {
      const i = cursor++;
      return (slots[i] ??= { current: initial });
    },
    useCallback(fn, deps) {
      const i = cursor++;
      if (!slots[i] || !equal(slots[i].deps, deps)) slots[i] = { fn, deps };
      return slots[i].fn;
    },
    useEffectEvent(fn) {
      const i = cursor++;
      if (!slots[i]) slots[i] = { call: (...args) => slots[i].fn(...args) };
      slots[i].fn = fn;
      return slots[i].call;
    },
    useEffect(fn, deps) {
      const i = cursor++;
      const previous = slots[i];
      slots[i] = { kind: 'effect', fn, deps, cleanup: previous?.cleanup };
      if (!previous || !equal(previous.deps, deps)) pending.push(i);
    }
  };
  const jsx = (type, props) => ({ type, props });
  const js = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
      jsx: ts.JsxEmit.ReactJSX
    }
  }).outputText;
  const exports = {};
  const require = (name) => {
    if (name === 'react') return react;
    if (name === 'react/jsx-runtime') return { jsx, jsxs: jsx, Fragment: 'Fragment' };
    if (name === 'react-i18next') return { useTranslation: () => ({ t: translate }) };
    if (name in modules) return modules[name];
    throw new Error(`Missing fixture module: ${name}`);
  };
  new Function('exports', 'require', ...Object.keys(globals), js)(
    exports,
    require,
    ...Object.values(globals)
  );
  function render(next = props) {
    props = next;
    for (let pass = 0; pass < 20; pass++) {
      cursor = 0;
      pending = [];
      dirty = false;
      output = exports[exportName](props);
      for (const i of pending) slots[i].cleanup?.();
      for (const i of pending) slots[i].cleanup = slots[i].fn();
      if (!dirty) return;
    }
    throw new Error('Effect triggered a render loop');
  }
  return {
    render,
    async flush() {
      await settle();
      render();
      await settle();
      render();
    },
    replay() {
      const effects = slots.filter((slot) => slot.kind === 'effect');
      effects.forEach((slot) => slot.cleanup?.());
      effects.forEach((slot) => {
        slot.cleanup = slot.fn();
      });
      render();
    },
    locale(locale) {
      translate = (key) => `${locale}:${key}`;
      render();
    },
    unmount() {
      slots.filter((slot) => slot.kind === 'effect').forEach((slot) => slot.cleanup?.());
    },
    get output() {
      return output;
    }
  };
}

function find(node, type) {
  if (!node || typeof node !== 'object') return;
  if (node.type === type) return node;
  for (const child of [node.props?.children].flat(Infinity)) {
    const found = find(child, type);
    if (found) return found;
  }
}
const noOp = () => {};

function scriptHarness() {
  const calls = [];
  const h = harness('../src/pages/desktop/menu/script/run.tsx', 'Run', {
    '@ant-design/icons': { LoadingOutlined: 'Loading' },
    antd: { Button: 'Button', Card: 'Card', Modal: 'Modal', Spin: 'Spin' },
    '@/api/script': {
      runScript: (script) => {
        const call = { script, ...deferred() };
        calls.push(call);
        return call.promise;
      }
    }
  });
  return { h, calls };
}

test('script effect replay and language changes do not execute the command again', async () => {
  const { h, calls } = scriptHarness();
  h.render({ script: 'one.sh', setIsRunning: noOp });
  h.replay();
  h.locale('ru');
  assert.equal(calls.length, 1);
  calls[0].reject(new Error('offline'));
  await h.flush();
  assert.equal(find(h.output, 'Card').props.children, 'ru:script.runFailed');
  h.locale('en');
  await h.flush();
  assert.equal(calls.length, 1);
  h.unmount();
});

test('changing a script executes it once and ignores the previous late result', async () => {
  const { h, calls } = scriptHarness();
  h.render({ script: 'one.sh', setIsRunning: noOp });
  h.render({ script: 'two.sh', setIsRunning: noOp });
  calls[1].resolve({ code: 0, data: { log: 'second result' } });
  await h.flush();
  calls[0].resolve({ code: 0, data: { log: 'obsolete result' } });
  await h.flush();
  assert.deepEqual(
    calls.map((call) => call.script),
    ['one.sh', 'two.sh']
  );
  assert.equal(find(h.output, 'Card').props.children, 'second result');
  h.unmount();
});

test('Tailscale status completion does not start a new automatic fetch', async () => {
  const requests = [];
  const h = harness('../src/pages/desktop/menu/settings/tailscale/index.tsx', 'Tailscale', {
    antd: { Divider: 'Divider' },
    'lucide-react': { LoaderCircleIcon: 'Loading' },
    '@/api/extensions/tailscale.ts': {
      getStatus: () => {
        const request = deferred();
        requests.push(request);
        return request.promise;
      }
    },
    './header.tsx': { Header: 'Header' },
    './device.tsx': { Device: 'Device' },
    './install.tsx': { Install: 'Install' },
    './login.tsx': { Login: 'Login' },
    './run.tsx': { Run: 'Run' }
  });
  h.render({ setIsLocked: noOp });
  h.replay();
  assert.equal(requests.length, 1);
  requests[0].resolve({ code: 0, data: { state: 'running' } });
  await h.flush();
  h.locale('ru');
  await h.flush();
  assert.equal(requests.length, 1);
  assert.equal(find(h.output, 'Device').props.status.state, 'running');
  find(h.output, 'Header').props.onSuccess();
  h.render();
  assert.equal(requests.length, 2);
  requests[1].resolve({ code: 0, data: { state: 'stopped' } });
  await h.flush();
  assert.equal(requests.length, 2);
  h.unmount();
});

test('memory effect replay and locale changes discard stale reads and keep one poller', async () => {
  const requests = [],
    pollers = new Set();
  const h = harness('../src/pages/desktop/menu/settings/memory/index.tsx', 'Memory', {
    '@/lib/swap-request.ts': { swapRequestSize },
    antd: {
      Alert: 'Alert',
      Progress: 'Progress',
      Select: 'Select',
      Spin: 'Spin',
      Switch: 'Switch'
    },
    '@/lib/visible-poll.ts': {
      pollWhileVisible: (fn) => {
        pollers.add(fn);
        return () => pollers.delete(fn);
      }
    },
    '@/api/vm.ts': {
      getMemoryStatus: () => {
        const request = deferred();
        requests.push(request);
        return request.promise;
      }
    }
  });
  h.render();
  h.replay();
  h.locale('ru');
  assert.equal(pollers.size, 1);
  assert.equal(requests.length, 1);
  requests[0].resolve({ code: 1, msg: 'stale error' });
  await h.flush();
  assert.equal(find(h.output, 'Alert'), undefined);
  assert.equal(requests.length, 2);
  requests[1].resolve({ code: 1, msg: 'current error' });
  await h.flush();
  assert.equal(find(h.output, 'Alert').props.message, 'current error');
  const poll = [...pollers][0];
  poll();
  poll();
  await h.flush();
  assert.equal(requests.length, 3);
  h.unmount();
  assert.equal(pollers.size, 0);
  requests[2].resolve({ code: 1, msg: 'after unmount' });
  await settle();
});

test('changing the terminal language does not reconnect or dispose its session', () => {
  const sockets = [],
    listeners = new Map();
  let disposed = 0;
  class Socket {
    static OPEN = 1;
    static CONNECTING = 0;
    readyState = 0;
    closed = 0;
    constructor(url) {
      this.url = url;
      sockets.push(this);
    }
    addEventListener() {}
    close() {
      this.closed++;
      this.readyState = 3;
    }
  }
  class Xterm {
    parser = { registerOscHandler: () => ({ dispose: noOp }) };
    loadAddon() {}
    open() {}
    dispose() {
      disposed++;
    }
  }
  const h = harness(
    '../src/pages/terminal/index.tsx',
    'Terminal',
    {
      '@xterm/addon-attach': { AttachAddon: class {} },
      '@xterm/addon-fit': {
        FitAddon: class {
          fit() {}
        }
      },
      '@xterm/xterm': { Terminal: Xterm },
      '@xterm/xterm/css/xterm.css': {},
      '@/lib/auth-events.ts': { notifyAuthExpired: noOp },
      '@/lib/service.ts': { getBaseUrl: () => 'ws://fixture' },
      '@/components/head.tsx': { Head: 'Head' },
      './launch.ts': { readSerialSession: () => new URLSearchParams() },
      './validater.ts': { buildSerialQuery: noOp }
    },
    {
      document: { getElementById: () => ({}) },
      WebSocket: Socket,
      ResizeObserver: class {
        observe() {}
        disconnect() {}
      },
      window: {
        addEventListener: (name, fn) => listeners.set(name, fn),
        removeEventListener: (name) => listeners.delete(name)
      }
    }
  );
  h.render();
  h.locale('ru');
  h.locale('en');
  assert.equal(sockets.length, 1);
  assert.equal(sockets[0].closed, 0);
  assert.equal(disposed, 0);
  assert.equal(listeners.size, 2);
  h.unmount();
  assert.equal(sockets[0].closed, 1);
  assert.equal(disposed, 1);
  assert.equal(listeners.size, 0);
});
