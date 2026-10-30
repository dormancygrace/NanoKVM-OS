import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

import { requestErrorText } from '../src/lib/request-error.ts';
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
    useMemo(fn, deps) {
      return react.useCallback(fn, deps)();
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

function allNodes(node, type) {
  if (!node || typeof node !== 'object') return [];
  return [
    ...(node.type === type ? [node] : []),
    ...[node.props?.children].flat(Infinity).flatMap((child) => allNodes(child, type))
  ];
}

for (const [label, method, failure, expected] of [
  ['network', 'reject', new Error('network offline'), 'en:image.remote.connectFailed'],
  [
    'API',
    'resolve',
    { code: -1, msg: 'Unmount the current image first' },
    'en:image.remote.connectFailed: Unmount the current image first'
  ],
  [
    'HTTP',
    'reject',
    { isAxiosError: true, response: { status: 409, data: { msg: 'Enable USB storage' } } },
    'en:image.remote.connectFailed: Enable USB storage'
  ]
])
  test(`remote URL form selects SMB and retains ${label} error detail while clearing its password`, async () => {
    const calls = [];
    const pending = deferred();
    const h = harness(
      '../src/pages/desktop/menu/image/remote.tsx',
      'RemoteImage',
      {
        antd: { Button: 'Button', Input: { Password: 'Password' }, Alert: 'Alert' },
        '@/api/storage.ts': {
          getRemoteMedia: async () => ({ code: 0, data: { connected: false } }),
          connectHTTPImage: (options) => {
            calls.push(['http', options]);
            return pending.promise;
          },
          connectSMBImage: (options) => {
            calls.push(['smb', options]);
            return pending.promise;
          }
        },
        '@/lib/service.ts': { getBaseUrl: () => 'https://device' },
        '@/lib/request-error.ts': { requestErrorText },
        '@/lib/visible-poll.ts': { pollWhileVisible: () => noOp }
      },
      { window: { addEventListener: noOp, removeEventListener: noOp, dispatchEvent: noOp } }
    );
    h.render({ isOpen: true, source: 'url', onConnected: noOp });
    await h.flush();
    const inputs = () =>
      allNodes(h.output, 'label')
        .flatMap((label) => [label.props.children].flat())
        .filter((n) => n?.props?.onChange);
    inputs()[0].props.onChange({ target: { value: 'smb://nas/images/test.iso' } });
    h.render();
    assert.equal(find(h.output, 'Button').props.disabled, true);
    inputs()[1].props.onChange({ target: { value: 'reader' } });
    h.render();
    // Domain follows the username, and Password is the final field.
    inputs()[2].props.onChange({ target: { value: 'OFFICE' } });
    find(h.output, 'Password').props.onChange({ target: { value: 'private-password' } });
    h.render();
    assert.equal(find(h.output, 'Button').props.disabled, false);
    const connecting = find(h.output, 'Button').props.onClick();
    assert.deepEqual(calls, [
      [
        'smb',
        {
          url: 'smb://nas/images/test.iso',
          username: 'reader',
          password: 'private-password',
          domain: 'OFFICE'
        }
      ]
    ]);
    pending[method](failure);
    await connecting;
    await h.flush();
    assert.equal(find(h.output, 'Password').props.value, '');
    assert.equal(find(h.output, 'Alert').props.message, expected);
    h.unmount();
  });

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
    '@/lib/theme-tokens.ts': { themeTokens: { info: '#38bdf8' } },
    '@/components/ui/settings.tsx': { Panel: 'Panel', SettingRow: 'SettingRow' },
    antd: {
      Alert: 'Alert',
      Checkbox: 'Checkbox',
      message: { error: noOp },
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

test('memory page shows the size of the video memory and whether Linux shares it', async () => {
  const swap = { enabled: false, available: true, sizeMiB: 64, usedBytes: 0 };
  const status = (shared, sizeMiB) => ({
    code: 0,
    data: {
      totalBytes: 1,
      usedBytes: 0,
      availableBytes: 1,
      cachedBytes: 0,
      videoBytes: 0,
      zram: swap,
      sd: swap,
      videoMemory: { sizeMiB, shared }
    }
  });
  let current = status(true, 144);
  const h = harness('../src/pages/desktop/menu/settings/memory/index.tsx', 'Memory', {
    '@/lib/swap-request.ts': { swapRequestSize },
    '@/lib/theme-tokens.ts': { themeTokens: { info: '#38bdf8' } },
    '@/components/ui/settings.tsx': { Panel: 'Panel', SettingRow: 'SettingRow' },
    antd: {
      Alert: 'Alert',
      message: { error: noOp },
      Progress: 'Progress',
      Select: 'Select',
      Spin: 'Spin',
      Switch: 'Switch'
    },
    '@/lib/visible-poll.ts': { pollWhileVisible: () => noOp },
    '@/api/vm.ts': { getMemoryStatus: async () => current }
  });
  const line = (key) => {
    const walk = (node) => {
      if (!node || typeof node !== 'object') return;
      if (node.props?.children === key) return node;
      for (const child of [node.props?.children].flat(Infinity)) {
        const found = walk(child);
        if (found) return found;
      }
    };
    return walk(h.output);
  };
  h.render();
  await h.flush();
  const shared = line('en:settings.memory.videoMemoryShared');
  assert.equal(shared.type, 'p');
  assert.doesNotMatch(shared.props.className, /text-warning/);
  assert.equal(line('en:settings.memory.videoMemoryReserved'), undefined);

  // A device updated from a fixed carveout until it restarts: no choice, a hint.
  current = status(false, 56);
  h.locale('ru');
  await h.flush();
  assert.equal(line('ru:settings.memory.videoMemoryShared'), undefined);
  assert.match(line('ru:settings.memory.videoMemoryReserved').props.className, /text-warning/);
  h.unmount();
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

function updatesHarness() {
  const reads = [],
    writes = [],
    notices = [];
  let poll;
  const setNotice = (value) => notices.push(value);
  const h = harness('../src/pages/desktop/menu/settings/updates/index.tsx', 'Updates', {
    antd: { Alert: 'Alert', message: { error: noOp } },
    jotai: { useSetAtom: () => setNotice },
    '@/jotai/updates.ts': { updateNoticeAtom: {} },
    '@/lib/http': {
      http: {
        get: () => {
          const d = deferred();
          reads.push(d);
          return d.promise;
        },
        post: () => {
          const d = deferred();
          writes.push(d);
          return d.promise;
        }
      }
    },
    '@/lib/version': { formatVersion: (v) => v },
    '@/lib/visible-poll.ts': {
      pollWhileVisible: (fn) => {
        poll = fn;
        return noOp;
      }
    },
    '@/components/ui/settings.tsx': { SettingRow: 'Row' },
    './card': { UpdateCard: 'Card' },
    './components': { ComponentList: 'Components', Fold: 'Fold' }
  });
  const reply = (notice) => ({ code: 0, data: { installed: { version: '3.3.4' }, notice } });
  return { h, reads, writes, notices, poll: () => poll(), reply };
}

test(
  'Updates skips overlapping poll ticks and ignores a read after unmount',
  { timeout: 2000 },
  async () => {
    const { h, reads, notices, poll, reply } = updatesHarness();
    h.render();
    poll();
    poll();
    assert.equal(reads.length, 1);
    h.unmount();
    reads[0].resolve(reply({ state: 'ok', updates: [] }));
    await settle();
    assert.deepEqual(notices, []);
  }
);

test(
  'Updates rejects a stale response when a post-action refresh finishes first',
  { timeout: 2000 },
  async () => {
    const { h, reads, writes, notices, poll, reply } = updatesHarness();
    h.render();
    reads[0].resolve(reply({ state: 'never' }));
    await h.flush();
    poll(); // Slow read predates the action.
    const action = find(h.output, 'Card').props.action('apk/check');
    writes[0].resolve({ code: 0 });
    await settle();
    assert.equal(reads.length, 3);
    reads[2].resolve(reply({ state: 'ok', checkedAt: 200 }));
    await action;
    await h.flush();
    reads[1].resolve(reply({ state: 'checking', checkedAt: 100 }));
    await h.flush();
    assert.equal(find(h.output, 'Card').props.notice.checkedAt, 200);
    assert.equal(notices.at(-1).checkedAt, 200);
  }
);

test(
  'Updates effect replay ignores the first read and remains pollable',
  { timeout: 2000 },
  async () => {
    const { h, reads, notices, poll, reply } = updatesHarness();
    h.render();
    h.replay();
    reads[1].resolve(reply({ state: 'ok', checkedAt: 200 }));
    await h.flush();
    reads[0].reject(new Error('old connection'));
    await h.flush();
    assert.equal(notices.at(-1).checkedAt, 200);
    assert.equal(find(h.output, 'Alert'), undefined);
    poll();
    assert.equal(reads.length, 3);
    h.unmount();
    reads[2].resolve(reply({ state: 'ok' }));
    await settle();
  }
);

test(
  'Updates action completing after unmount cannot start another status request',
  { timeout: 2000 },
  async () => {
    const { h, reads, writes, reply } = updatesHarness();
    h.render();
    reads[0].resolve(reply({ state: 'never' }));
    await h.flush();
    const action = find(h.output, 'Card').props.action('apk/check');
    h.unmount();
    writes[0].resolve({ code: 0 });
    await action;
    assert.equal(reads.length, 1);
  }
);

function notificationHarness() {
  const reads = [];
  let notice = null,
    poll;
  const setNotice = (next) => {
    notice = typeof next === 'function' ? next(notice) : next;
  };
  const api = { info: noOp, destroy: noOp };
  const h = harness('../src/pages/desktop/update-notification.tsx', 'UpdateNotification', {
    '@/contexts/auth.ts': { useAuth: () => ({ account: { role: 'admin' } }) },
    antd: { Button: 'Button', notification: { useNotification: () => [api, null] } },
    jotai: { useAtom: () => [notice, setNotice], useSetAtom: () => noOp },
    '@/api/updates.ts': {
      availableUpdates: (n) => n?.updates ?? [],
      getUpdateNotice: () => {
        const d = deferred();
        reads.push(d);
        return d.promise;
      }
    },
    '@/lib/update-components.ts': {
      getSeenUpdates: noOp,
      setSeenUpdates: noOp,
      updateSignature: () => ''
    },
    '@/lib/visible-poll.ts': {
      pollWhileVisible: (fn) => {
        poll = fn;
        return noOp;
      }
    },
    '@/jotai/settings.ts': { settingsRequestAtom: {} },
    '@/jotai/updates.ts': { updateNoticeAtom: {} }
  });
  return {
    h,
    reads,
    setNotice,
    poll: () => poll(),
    get notice() {
      return notice;
    }
  };
}

test(
  'notification polling cannot overwrite newer settings state or overlap itself',
  { timeout: 2000 },
  async () => {
    const fixture = notificationHarness();
    fixture.h.render();
    fixture.poll();
    fixture.poll();
    assert.equal(fixture.reads.length, 1);
    const newer = { state: 'ok', checkedAt: 200, updates: [] };
    fixture.setNotice(newer);
    fixture.reads[0].resolve({ code: 0, data: { state: 'ok', checkedAt: 100, updates: [] } });
    await fixture.h.flush();
    assert.equal(fixture.notice, newer);
    fixture.poll();
    assert.equal(fixture.reads.length, 2);
    const latest = { state: 'ok', checkedAt: 300, updates: [] };
    fixture.reads[1].resolve({ code: 0, data: latest });
    await fixture.h.flush();
    assert.equal(fixture.notice, latest);
    fixture.h.unmount();
  }
);
