import assert from 'node:assert/strict';
import fs from 'node:fs';
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
        if (type === Keyboard && !keyboard) keyboard = props;
        return null;
      },
      jsxs: (type, props) => {
        if (type === Keyboard && !keyboard) keyboard = props;
        return null;
      },
      Fragment: element
    },
    '@ant-design/icons': { AppleOutlined: element, WindowsOutlined: element },
    clsx: (...names) => names.join(' '),
    jotai: { useAtom: () => [true, () => {}] },
    'lucide-react': { XIcon: element },
    'react-simple-keyboard/build/index.modern.esm.js': Keyboard,
    vaul: { Drawer: { Root: element, Portal: element, Content: element } },
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
    pressed,
    press: (key) => keyboard.onKeyPress(key),
    releaseOnKey: (key) => keyboard.onKeyReleased(key),
    pointerUp: () => fire(documentEvents, 'pointerup'),
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
  h.press('a');
  h.releaseOnKey('a');
  const count = h.sent.length;
  h.pointerUp();
  assert.equal(h.sent.length, count);
  assert.ok(!h.pressed());
});

for (const lost of ['blur', 'pagehide', 'unmount'])
  test(`${lost} with a key down releases it`, () => {
    const h = harness();
    h.press('{enter}');
    assert.ok(h.pressed());
    if (lost === 'unmount') h.unmount();
    else h.window(lost);
    assert.ok(!h.pressed());
  });
