import assert from 'node:assert/strict';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

import * as keymap from '../src/lib/keymap.ts';
import * as virtualKeys from '../src/pages/desktop/virtual-keyboard/virtual-keys.ts';

// The virtual keyboard with its document and window; simple-keyboard is the
// first <Keyboard>, whose callbacks the test calls as a pointer would.
function harness() {
  const windowEvents = new Map(),
    documentEvents = new Map(),
    sent = [];
  const add = (map) => (name, fn) => map.set(name, [...(map.get(name) ?? []), fn]);
  const remove = (map) => (name, fn) =>
    map.set(
      name,
      (map.get(name) ?? []).filter((f) => f !== fn)
    );
  const Keyboard = () => null;
  let keyboard;
  const keyboards = [];
  const cleanups = [];
  const element = () => null;
  const modules = {
    react: {
      useRef: (value) => ({ current: value }),
      useState: (value) => [value, () => {}],
      useEffect: (fn) => {
        const cleanup = fn();
        if (cleanup) cleanups.push(cleanup);
      }
    },
    'react/jsx-runtime': {
      jsx: (type, props) => {
        if (type === Keyboard) {
          keyboards.push(props);
          if (!keyboard) keyboard = props;
        }
        return null;
      },
      jsxs: (type, props) => {
        if (type === Keyboard) {
          keyboards.push(props);
          if (!keyboard) keyboard = props;
        }
        return null;
      },
      Fragment: element
    },
    '@ant-design/icons': { AppleOutlined: element, WindowsOutlined: element },
    clsx: (...names) => names.join(' '),
    jotai: { useAtom: () => [true, () => {}] },
    'lucide-react': { XIcon: element },
    'react-simple-keyboard/build/index.modern.esm.js': Keyboard,
    './keyboard-sheet.tsx': { KeyboardSheet: element },
    'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
    'react-simple-keyboard/build/css/index.css': {},
    '@/assets/styles/keyboard.css': {},
    antd: { ConfigProvider: element, Segmented: element, Select: element, theme: {} },
    'react-responsive': { useMediaQuery: () => true },
    '@/lib/keymap.ts': keymap,
    '@/lib/localstorage.ts': { getKeyboardSystem: () => 'win', getKeyboardLanguage: () => 'en' },
    '@/lib/websocket.ts': {
      client: { send: (data) => sent.push([...data].slice(1)) },
      MessageEvent: { Keyboard: 1 }
    },
    '@/jotai/keyboard.ts': { isKeyboardOpenAtom: 'open' },
    './virtual-keys.ts': virtualKeys
  };
  const code = ts.transpileModule(
    fs.readFileSync(
      new URL('../src/pages/desktop/virtual-keyboard/index.tsx', import.meta.url),
      'utf8'
    ),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }
  ).outputText;
  const document = {
    visibilityState: 'visible',
    addEventListener: add(documentEvents),
    removeEventListener: remove(documentEvents)
  };
  const exports = {};
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      assert.ok(name in modules, name);
      return modules[name];
    },
    document,
    window: { addEventListener: add(windowEvents), removeEventListener: remove(windowEvents) },
    console
  });
  exports.VirtualKeyboard();
  const fire = (map, name) => {
    for (const fn of map.get(name) ?? []) fn({});
  };
  const pressed = () => sent.length > 0 && sent.at(-1)[2] !== 0;
  return {
    sent,
    keyboards,
    pressed,
    press: (key) => keyboard.onKeyPress(key),
    releaseOnKey: (key) => keyboard.onKeyReleased(key),
    pointerUp: () => fire(documentEvents, 'pointerup'),
    pointerCancel: () => fire(documentEvents, 'pointercancel'),
    hidden: () => {
      document.visibilityState = 'hidden';
      fire(documentEvents, 'visibilitychange');
    },
    window: (name) => fire(windowEvents, name),
    unmount: () => cleanups.forEach((cleanup) => cleanup())
  };
}

test('a key released off its button is released on the host', () => {
  const h = harness();
  h.press('{backspace}');
  assert.ok(h.pressed());
  // The pointer went up over another key or the video: no onKeyReleased.
  h.pointerUp();
  assert.ok(!h.pressed());
  const count = h.sent.length;
  h.pointerUp();
  assert.equal(h.sent.length, count, 'nothing to release twice');
});

test('a key released on its button is released once', () => {
  const h = harness();
  h.press('KeyA');
  assert.ok(h.pressed());
  h.releaseOnKey('KeyA');
  assert.equal(h.sent.length, 2, 'one keydown and one keyup');
  const count = h.sent.length;
  h.pointerUp();
  assert.equal(h.sent.length, count);
  assert.ok(!h.pressed());
});

for (const lost of ['blur', 'pagehide', 'unmount', 'hidden', 'pointerCancel'])
  test(`${lost} with a key down releases it`, () => {
    const h = harness();
    h.press('{enter}');
    assert.ok(h.pressed());
    if (['unmount', 'hidden', 'pointerCancel'].includes(lost)) h[lost]();
    else h.window(lost);
    assert.ok(!h.pressed());
  });

// Exercise the hold timer shipped inside react-simple-keyboard, without a DOM.
// The package bundles this dependency; its source map exposes the exact UMD module.
function libraryHoldTimer(options) {
  const require = createRequire(import.meta.url);
  const map = JSON.parse(
    fs.readFileSync(require.resolve('react-simple-keyboard/build/index.modern.esm.js.map'), 'utf8')
  );
  const source =
    map.sourcesContent[
      map.sources.findIndex((name) => name.endsWith('simple-keyboard/build/index.modern.js'))
    ];
  assert.ok(source, 'the bundled simple-keyboard implementation is available');
  let now = 0,
    nextId = 0;
  const timers = new Map();
  const module = { exports: {} };
  vm.runInNewContext(source, {
    module,
    exports: module.exports,
    console,
    window: {
      setTimeout(fn, delay) {
        const id = ++nextId;
        timers.set(id, { at: now + delay, fn });
        return id;
      }
    },
    clearTimeout: (id) => timers.delete(id)
  });
  const Library = module.exports.default ?? module.exports;
  const keyboard = Object.create(Library.prototype);
  keyboard.options = options;
  keyboard.handleButtonClicked = (key) => options.onKeyPress(key);
  return {
    down(key) {
      keyboard.handleButtonMouseDown(key);
      keyboard.handleButtonClicked(key);
    },
    advance(ms) {
      const end = now + ms;
      while ([...timers.values()].some((timer) => timer.at <= end)) {
        const [id, timer] = [...timers].sort((a, b) => a[1].at - b[1].at)[0];
        timers.delete(id);
        now = timer.at;
        timer.fn();
      }
      now = end;
    }
  };
}

for (const [index, key] of [
  [0, 'KeyA'],
  [1, '{delete}'],
  [2, '{arrowup}']
])
  for (const lost of ['blur', 'pagehide', 'hidden', 'unmount'])
    test('keyboard ' + index + ' cannot re-press a key after ' + lost, () => {
      const h = harness();
      const timer = libraryHoldTimer(h.keyboards[index]);
      timer.down(key);
      assert.ok(h.pressed());
      timer.advance(800); // Also cover a repeat that had already started.
      if (lost === 'hidden' || lost === 'unmount') h[lost]();
      else h.window(lost);
      assert.ok(!h.pressed());
      const count = h.sent.length;
      timer.advance(1500);
      assert.equal(h.sent.length, count, 'no late keydown after release');
      assert.ok(!h.pressed());
    });
