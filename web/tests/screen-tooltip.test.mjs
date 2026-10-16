import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import ts from 'typescript';

// Exercise the real component props without a DOM. Browser verification covers
// the antd event handlers and hit areas; these cases protect the session states
// and accessible trigger configuration from future toolbar refactors.
function render(path, name, { sessions = null, props = {} } = {}) {
  const jsx = (type, props) => ({ type, props });
  const exports = {};
  const js = ts.transpileModule(readFileSync(new URL(path, import.meta.url), 'utf8'), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX }
  }).outputText;
  new Function('exports', 'require', js)(exports, module => {
    if (module === 'react/jsx-runtime') return { jsx, jsxs: jsx };
    if (module === 'react') return {
      useState: value => [value, () => {}], useRef: value => ({ current: value }),
      useCallback: fn => fn, useEffect() {}, useContext: () => ({}),
      useMemo: fn => fn(), useId: () => 'test'
    };
    if (module === 'antd') return { Tooltip: 'Tooltip', Popover: 'Popover', Button: 'Button' };
    if (module === 'jotai') return {
      useAtomValue: atom => atom === 'sessions' ? sessions : 0,
      useSetAtom: () => () => {}
    };
    if (module === 'react-i18next') return { useTranslation: () => ({
      t: (key, values) => key === 'screen.sessions'
        ? 'Active video sessions: ' + values.count : key === 'screen.title' ? 'Screen' : key
    }) };
    if (module === 'react-responsive') return { useMediaQuery: () => true };
    if (module === 'clsx') return { __esModule: true, default: () => '' };
    if (module.includes('jotai/screen')) return { videoSessionCountAtom: 'sessions' };
    if (module.includes('lib/encoder')) return { getEncoderCodec: () => 'h264' };
    if (module.includes('useResponsiveDevice')) return { useResponsiveDevice: () => ({}) };
    if (module.includes('mobile-menu-context')) return { useDismissMobileMenu: () => () => {} };
    return {};
  });
  return exports[name](props);
}

function find(node, predicate) {
  if (!node || typeof node !== 'object') return;
  if (predicate(node)) return node;
  const children = node.props?.children;
  for (const child of Array.isArray(children) ? children : [children]) {
    const found = find(child, predicate);
    if (found) return found;
  }
}

for (const count of [0, 2, 123]) {
  test('Screen tooltip and button name explain the full session count: ' + count, () => {
    const screen = render('../src/pages/desktop/menu/screen/index.tsx', 'Screen', { sessions: count });
    assert.match(screen.props.title, /Screen/);
    assert.ok(screen.props.title.includes('Active video sessions: ' + count));
    const menu = render('../src/components/menu-item.tsx', 'MenuItem', { props: screen.props });
    assert.equal(find(menu, node => node.type === 'Tooltip').props.title, screen.props.title);
    assert.equal(find(menu, node => node.props?.role === 'button').props['aria-label'], screen.props.title);
    const badge = find(screen.props.icon, node => node.props?.children === (count > 99 ? '99+' : count));
    assert.ok(badge, 'badge still caps only its visual count');
    assert.equal(badge.props['aria-hidden'], 'true');
  });
}

test('unknown session count keeps the Screen label without inventing a count', () => {
  const screen = render('../src/pages/desktop/menu/screen/index.tsx', 'Screen');
  assert.equal(screen.props.title, 'Screen');
});

test('shared tooltip supports hover and keyboard focus; menu still opens on click', () => {
  const menu = render('../src/components/menu-item.tsx', 'MenuItem', { props: {
    title: 'Screen', icon: null, content: null
  } });
  const tooltip = find(menu, node => node.type === 'Tooltip');
  assert.ok((tooltip.props.trigger ?? ['hover']).includes('hover'));
  assert.ok(tooltip.props.trigger?.includes('focus'), 'keyboard focus opens the tooltip');
  assert.equal(menu.type, 'Popover');
  assert.equal(menu.props.trigger, 'click');
  assert.equal(tooltip.props.open, false, 'tooltip initially closed');
});
