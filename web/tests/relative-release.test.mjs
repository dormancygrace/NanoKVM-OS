import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

import { MouseReportRelative } from '../src/lib/mouse.ts';

// The relative mouse component with its document, window and screen.
function harness() {
  const screenEvents = new Map(),
    windowEvents = new Map(),
    documentEvents = new Map(),
    reports = [];
  const add = (map) => (name, fn) => map.set(name, [...(map.get(name) ?? []), fn]);
  const remove = (map) => (name, fn) =>
    map.set(
      name,
      (map.get(name) ?? []).filter((f) => f !== fn)
    );
  const screen = {
    style: {},
    contains: (node) => node === screen,
    addEventListener: add(screenEvents),
    removeEventListener: remove(screenEvents)
  };
  const document = {
    visibilityState: 'visible',
    pointerLockElement: null,
    getElementById: () => screen,
    exitPointerLock() {},
    addEventListener: add(documentEvents),
    removeEventListener: remove(documentEvents)
  };
  let cleanup;
  const exports = {};
  const atoms = {
    inputAdapterAtom: 'mouse',
    scrollDirectionAtom: 1,
    scrollIntervalAtom: 0,
    resolutionAtom: { width: 1000, height: 1000 }
  };
  const modules = {
    react: {
      useRef: (value) => ({ current: value }),
      useEffect: (fn) => {
        cleanup = fn();
      },
      useEffectEvent: (fn) => fn
    },
    'react/jsx-runtime': { jsx: () => null, Fragment: () => null },
    antd: { message: { useMessage: () => [new Proxy({}, { get: () => () => {} }), null] } },
    jotai: { useAtomValue: (value) => value },
    'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
    '@/lib/input-adapter.ts': { resolveInputAdapter: () => 'mouse' },
    '@/lib/mouse.ts': { MouseReportRelative },
    '@/lib/websocket.ts': {
      client: { send: (data) => reports.push([...data].slice(1)) },
      MessageEvent: { Mouse: 2 }
    },
    '@/jotai/mouse.ts': atoms,
    '@/jotai/screen.ts': atoms,
    './types.ts': {}
  };
  const code = ts.transpileModule(
    fs.readFileSync(new URL('../src/pages/desktop/mouse/relative.tsx', import.meta.url), 'utf8'),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }
  ).outputText;
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      assert.ok(modules[name], name);
      return modules[name];
    },
    document,
    window: {
      devicePixelRatio: 1,
      addEventListener: add(windowEvents),
      removeEventListener: remove(windowEvents)
    },
    navigator: {},
    Date,
    setTimeout,
    clearTimeout
  });
  exports.Relative();
  const fire = (map, name, event = {}) => {
    for (const fn of map.get(name) ?? [])
      fn({ preventDefault() {}, stopPropagation() {}, ...event });
  };
  return {
    reports,
    buttons: () => reports.at(-1)[0],
    press: (button) => fire(screenEvents, 'mousedown', { button, target: screen }),
    releaseOutside: (button) => fire(windowEvents, 'mouseup', { button, target: {} }),
    lock(locked) {
      document.pointerLockElement = locked ? screen : null;
      fire(documentEvents, 'pointerlockchange');
    },
    window: (name) => fire(windowEvents, name),
    hide() {
      document.visibilityState = 'hidden';
      fire(documentEvents, 'visibilitychange');
    },
    close: () => cleanup()
  };
}

test('losing the pointer lock with a button down releases it', () => {
  const h = harness();
  h.lock(true);
  h.press(0);
  assert.equal(h.buttons(), 1);
  h.lock(false);
  assert.equal(h.buttons(), 0);
  h.close();
});

for (const lost of ['blur', 'pagehide', 'hidden'])
  test(`${lost} with a button down releases it once`, () => {
    const h = harness();
    h.press(2);
    assert.equal(h.buttons(), 2);
    if (lost === 'hidden') h.hide();
    else h.window(lost);
    assert.equal(h.buttons(), 0);
    const count = h.reports.length;
    if (lost === 'hidden') h.hide();
    else h.window(lost);
    assert.equal(h.reports.length, count);
    h.close();
  });

test('a button released outside the screen is released on the host', () => {
  const h = harness();
  h.press(0);
  h.releaseOutside(0);
  assert.equal(h.buttons(), 0);
  const count = h.reports.length;
  h.releaseOutside(1);
  assert.equal(h.reports.length, count);
  h.close();
});
