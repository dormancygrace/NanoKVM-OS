import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { createStore } from 'jotai';
import ts from 'typescript';

import { isKeyboardOpenAtom } from '../src/jotai/keyboard.ts';
import { releasePointerMenuFocus } from '../src/lib/menu-focus.ts';

function fixture() {
  const store = createStore();
  let dismissals = 0;
  const jsx = (type, props) => ({ type, props });
  const exports = {};
  const source = readFileSync(
    new URL('../src/pages/desktop/menu/keyboard/index.tsx', import.meta.url),
    'utf8'
  );
  const js = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX }
  }).outputText;
  new Function('exports', 'require', js)(exports, (name) => {
    if (name === 'react')
      return {
        useContext: () => ({
          onRequestMobileMenuDismiss: () => {
            dismissals++;
          }
        })
      };
    if (name === 'react/jsx-runtime') return { jsx, jsxs: jsx, Fragment: 'fragment' };
    if (name === 'jotai')
      return { useAtom: (atom) => [store.get(atom), (value) => store.set(atom, value)] };
    if (name === 'react-i18next') return { useTranslation: () => ({ t: (key) => key }) };
    if (name.includes('contexts/auth')) return { useAuth: () => ({ account: { role: 'user' } }) };
    if (name.includes('jotai/keyboard')) return { isKeyboardOpenAtom };
    if (name.includes('menu-focus')) return { releasePointerMenuFocus };
    if (name.includes('components/menu-item')) return { MenuItem: 'MenuItem' };
    return {};
  });
  return {
    render: (props = {}) => exports.Keyboard(props).props.children.filter(Boolean),
    store,
    dismissals: () => dismissals
  };
}

test('desktop keyboard keeps its single menu; quick access has distinct accessible labels', () => {
  const f = fixture();
  for (const props of [{}, { quickAccess: false }]) {
    const nodes = f.render(props);
    assert.equal(nodes.length, 1);
    assert.equal(nodes[0].type, 'MenuItem');
    assert.equal(nodes[0].props.title, 'keyboard.title');
  }
  const [toggle, menu] = f.render({ quickAccess: true });
  assert.equal(toggle.type, 'button');
  assert.equal(toggle.props['aria-label'], 'keyboard.virtual');
  assert.equal(menu.props.title, 'keyboard.more');
  assert.notEqual(toggle.props['aria-label'], menu.props.title);
});

test('quick access reflects keyboard state, toggles it, and dismisses the mobile menu', () => {
  const f = fixture();
  let blurs = 0;
  const target = {
    blur: () => {
      blurs++;
    }
  };
  let [toggle] = f.render({ quickAccess: true });
  assert.equal(toggle.props['aria-pressed'], false);
  toggle.props.onClick({ detail: 1, currentTarget: target });
  assert.equal(f.store.get(isKeyboardOpenAtom), true);
  assert.equal(f.dismissals(), 1);
  assert.equal(blurs, 1);
  [toggle] = f.render({ quickAccess: true });
  assert.equal(toggle.props['aria-pressed'], true);
  toggle.props.onClick({ detail: 0, currentTarget: target });
  assert.equal(f.store.get(isKeyboardOpenAtom), false);
  assert.equal(f.dismissals(), 2);
  assert.equal(blurs, 1, 'keyboard activation retains focus');
  assert.equal(f.render({ quickAccess: true })[0].props['aria-pressed'], false);
});
