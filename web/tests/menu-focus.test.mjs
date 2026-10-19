import assert from 'node:assert/strict';
import test from 'node:test';
import { retryableLazy } from '../src/lib/retryable-lazy.ts';
import { releasePointerMenuFocus } from '../src/lib/menu-focus.ts';

for (const detail of [1, 2]) {
  test('pointer activation releases trigger before dialog remembers focus: ' + detail, () => {
    const body = {};
    const document = { activeElement: null };
    const trigger = { blur() { if (document.activeElement === this) document.activeElement = body; } };
    document.activeElement = trigger;
    releasePointerMenuFocus({ detail, currentTarget: trigger });
    // The dialog snapshots the current focus when opening and restores it on
    // close. Enter must not target the toolbar trigger after that restoration.
    const restoreTarget = document.activeElement;
    document.activeElement = {};
    document.activeElement = restoreTarget;
    assert.equal(document.activeElement, body);
  });
}

test('keyboard/assistive activation retains the trigger as the focus return target', () => {
  let blurred = false;
  releasePointerMenuFocus({ detail: 0, currentTarget: { blur() { blurred = true; } } });
  assert.equal(blurred, false);
});

async function renderComponent(path, exportName) {
  const { readFileSync } = await import('node:fs');
  const { default: ts } = await import('typescript');
  const jsx = (type, props) => ({ type, props });
  const exports = {};
  const js = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX }
  }).outputText;
  new Function('exports', 'require', js)(exports, name => {
    if (name === 'react') return {
      useState: value => [value, () => {}], useRef: value => ({ current: value }),
      useCallback: fn => fn, useEffect() {}, useContext: () => ({}),
      useMemo: fn => fn(), useId: () => 'test', lazy: () => () => null
    };
    if (name === 'react/jsx-runtime') return { jsx, jsxs: jsx, Fragment: 'fragment' };
    if (name === 'jotai') return {
      useAtom: () => [undefined, () => {}], useAtomValue: () => 0, useSetAtom: () => () => {}
    };
    if (name === 'react-i18next') return { useTranslation: () => ({ t: key => key }) };
    if (name === 'react-responsive') return { useMediaQuery: () => true };
    if (name === 'clsx') return { __esModule: true, default: () => '' };
    if (name.includes('retryable-lazy')) return { retryableLazy };
    if (name.includes('menu-focus')) return { releasePointerMenuFocus };
    if (name.includes('useVirtualDisk')) return { useVirtualDisk: () => true };
    if (name.includes('mobile-menu-context')) return { useDismissMobileMenu: () => () => {} };
    if (name.includes('useResponsiveDevice')) return { useResponsiveDevice: () => ({}) };
    if (name.includes('contexts/auth')) return { useAuth: () => ({ account: { role: 'user' } }) };
    if (name === './tabs.ts') return new Proxy({}, { get: () => () => undefined });
    return {};
  });
  return exports[exportName]({ title: 'test-menu', icon: null, content: null });
}

function findTrigger(node, label) {
  if (!node || typeof node !== 'object') return;
  if (node.props?.['aria-label'] === label) return node;
  const children = node.props?.children;
  for (const child of Array.isArray(children) ? children : [children]) {
    const found = findTrigger(child, label);
    if (found) return found;
  }
}

for (const [path, component, label] of [
  ['../src/components/menu-item.tsx', 'MenuItem', 'test-menu'],
  ['../src/pages/desktop/menu/image/index.tsx', 'Image', 'image.title'],
  ['../src/pages/desktop/menu/picoclaw/index.tsx', 'Picoclaw', 'picoclaw.title'],
  ['../src/pages/desktop/menu/settings/index.tsx', 'Settings', 'settings.title']
]) {
  test(component + ' pointer clicks release focus while keyboard clicks retain it', async () => {
    const tree = await renderComponent(path, component);
    const trigger = findTrigger(tree, label);
    assert.ok(trigger, 'accessible menu trigger exists');
    let blurs = 0;
    const currentTarget = { blur() { blurs++; } };
    trigger.props.onClick({ detail: 1, currentTarget });
    assert.equal(blurs, 1);
    trigger.props.onClick({ detail: 0, currentTarget });
    assert.equal(blurs, 1);
  });
}
