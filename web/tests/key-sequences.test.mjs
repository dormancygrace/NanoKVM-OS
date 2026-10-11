import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

// The key sequence modal follows a detached REISUB through the status route.
// Rendered at source level with fake hooks and timers (the harness of
// effect-lifecycle.test.mjs, reduced): no device I/O.

const settle = async () => {
  for (let i = 0; i < 10; i++) await Promise.resolve();
};

function harness(path, exportName, modules, globals) {
  const slots = [];
  let cursor = 0,
    pending = [],
    dirty = false,
    output;
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
    if (name === 'react-i18next') return { useTranslation: () => ({ t: (key) => key }) };
    if (name in modules) return modules[name];
    throw new Error(`Missing fixture module: ${name}`);
  };
  new Function('exports', 'require', ...Object.keys(globals), js)(
    exports,
    require,
    ...Object.values(globals)
  );
  function render() {
    for (let pass = 0; pass < 20; pass++) {
      cursor = 0;
      pending = [];
      dirty = false;
      output = exports[exportName]({});
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
    get output() {
      return output;
    }
  };
}

function findAll(node, type, found = []) {
  if (!node || typeof node !== 'object') return found;
  if (node.type === type) found.push(node);
  for (const child of [node.props?.children].flat(Infinity)) findAll(child, type, found);
  return found;
}

function setup() {
  const timers = new Map();
  let nextTimer = 1;
  const window = {
    setTimeout(fn) {
      const id = nextTimer++;
      timers.set(id, fn);
      return id;
    },
    clearTimeout(id) {
      timers.delete(id);
    }
  };
  const statusRequests = [];
  let serverStatus = { running: false };
  const messages = [];
  const h = harness(
    '../src/pages/desktop/menu/keyboard/sequences.tsx',
    'Sequences',
    {
      antd: {
        Button: 'Button',
        Divider: 'Divider',
        InputNumber: 'InputNumber',
        Modal: 'Modal',
        Progress: 'Progress',
        Select: 'Select',
        message: {
          success: (text) => messages.push(['success', text]),
          error: (text) => messages.push(['error', text])
        }
      },
      jotai: { useSetAtom: () => () => {} },
      'lucide-react': { ZapIcon: 'ZapIcon' },
      '@/api/hid.ts': {
        sendKeySequence: async () => ({ code: 0, data: { durationMs: 30000 } }),
        getKeySequenceStatus: async () => {
          statusRequests.push(serverStatus);
          return { code: 0, data: serverStatus };
        }
      },
      '@/jotai/keyboard.ts': { keyboardLockAtom: {} },
      '@/components/ui/confirm.ts': { confirmAction: async () => true }
    },
    { window }
  );
  const modal = () => findAll(h.output, 'Modal')[0];
  const buttons = () => findAll(h.output, 'Button');
  const open = async () => {
    findAll(h.output, 'div')[0].props.onClick();
    h.render();
    modal().props.afterOpenChange(true);
    await h.flush();
  };
  const close = async () => {
    modal().props.onCancel();
    h.render();
    modal().props.afterOpenChange(false);
    await h.flush();
  };
  const fireTimers = async () => {
    const due = [...timers.values()];
    timers.clear();
    due.forEach((fn) => fn());
    await h.flush();
  };
  return {
    h,
    timers,
    statusRequests,
    messages,
    setServer: (status) => (serverStatus = status),
    buttons,
    open,
    close,
    fireTimers
  };
}

test('reopening the modal after a REISUB finished re-enables the buttons and reports it', async () => {
  const s = setup();
  s.h.render();
  await s.open();
  assert.ok(s.buttons().every((b) => !b.props.disabled));

  s.setServer({ running: true, sequence: 'reisub', durationMs: 30000, remainingMs: 30000 });
  const reisub = s.buttons().find((b) => b.props.danger);
  await reisub.props.onClick();
  await s.h.flush();
  assert.equal(s.timers.size, 1, 'polling started');
  assert.ok(s.buttons().every((b) => b.props.disabled));

  await s.close();
  assert.equal(s.timers.size, 0, 'polling stopped on close');

  // The server finishes while the modal is closed.
  s.setServer({
    running: false,
    lastResult: { sequence: 'reisub', ok: true, finishedAt: '2026-10-12T00:00:00Z' }
  });
  const before = s.statusRequests.length;
  await s.open();
  assert.ok(s.statusRequests.length > before, 'status asked on reopen');
  assert.ok(
    s.buttons().every((b) => !b.props.disabled),
    'buttons enabled after the sequence ended'
  );
  assert.deepEqual(s.messages, [['success', 'keyboard.sequences.sent']]);
  assert.equal(s.timers.size, 0);
});

test('reopening while the REISUB still runs resumes polling until it ends', async () => {
  const s = setup();
  s.h.render();
  await s.open();
  s.setServer({ running: true, sequence: 'reisub', durationMs: 30000, remainingMs: 20000 });
  await s
    .buttons()
    .find((b) => b.props.danger)
    .props.onClick();
  await s.h.flush();
  await s.close();

  await s.open();
  assert.equal(s.timers.size, 1, 'polling resumed');
  assert.ok(s.buttons().every((b) => b.props.disabled));

  s.setServer({ running: true, sequence: 'reisub', durationMs: 30000, remainingMs: 5000 });
  await s.fireTimers();
  assert.equal(s.timers.size, 1);

  s.setServer({
    running: false,
    lastResult: { sequence: 'reisub', ok: false, error: 'x', finishedAt: '2026-10-12T00:00:00Z' }
  });
  await s.fireTimers();
  assert.equal(s.timers.size, 0);
  assert.ok(s.buttons().every((b) => !b.props.disabled));
  assert.deepEqual(s.messages, [['error', 'keyboard.sequences.failed']]);
});

test('a REISUB started elsewhere is followed without announcing its result', async () => {
  const s = setup();
  s.h.render();
  s.setServer({ running: true, sequence: 'reisub', durationMs: 30000, remainingMs: 10000 });
  await s.open();
  assert.equal(s.timers.size, 1);
  assert.ok(s.buttons().every((b) => b.props.disabled));
  s.setServer({
    running: false,
    lastResult: { sequence: 'reisub', ok: true, finishedAt: '2026-10-12T00:00:00Z' }
  });
  await s.fireTimers();
  assert.ok(s.buttons().every((b) => !b.props.disabled));
  assert.deepEqual(s.messages, []);
});
