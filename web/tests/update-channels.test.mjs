import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

function render(channel, operation = 'idle') {
  const calls = [];
  let stateIndex = 0;
  const jsx = (type, props) => ({ type, props });
  const modules = {
    react: {
      useState: () => [
        stateIndex++ === 0
          ? { channel, installed: { version: 'v3.4.0-beta.1' }, apk: { state: operation } }
          : false,
        () => {}
      ],
      useRef: (current) => ({ current }),
      useEffect: () => {},
      useCallback: (fn) => fn
    },
    'react/jsx-runtime': { jsx, jsxs: jsx },
    antd: {
      Alert: 'Alert',
      Button: 'Button',
      Select: 'Select',
      Popconfirm: 'Popconfirm',
      message: { error: assert.fail }
    },
    jotai: { useSetAtom: () => () => {} },
    'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
    '@/lib/http': {
      http: {
        post: async (...args) => {
          calls.push(args);
          return { code: 0 };
        }
      }
    },
    '@/lib/version': { formatVersion: (value) => value },
    '@/lib/visible-poll.ts': {},
    '@/jotai/updates.ts': {},
    '@/components/ui/settings.tsx': { SettingRow: 'SettingRow' },
    './apk': { APKUpdates: 'APKUpdates' },
    './components': { ComponentList: 'ComponentList', UpdateCheck: 'UpdateCheck' }
  };
  const exports = {};
  const source = readFileSync(
    new URL('../src/pages/desktop/menu/settings/updates/index.tsx', import.meta.url),
    'utf8'
  );
  const code = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX }
  }).outputText;
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      assert.ok(name in modules, name);
      return modules[name];
    }
  });
  const nodes = [];
  function visit(node) {
    if (!node || typeof node !== 'object') return;
    if (Array.isArray(node)) {
      node.forEach(visit);
      return;
    }
    nodes.push(node);
    visit(node.props?.children);
    visit(node.props?.action);
  }
  visit(exports.Updates());
  return { calls, nodes, find: (type) => nodes.find((node) => node.type === type)?.props };
}

test('channel selection only changes the repository; rollback needs separate confirmation', async () => {
  const view = render({ channel: 'stable', available: true, rollbackAvailable: true });
  await view.find('Select').onChange('beta');
  assert.equal(view.calls[0][0], '/api/os/update/channel');
  assert.equal(view.calls[0][1].channel, 'beta');
  assert.equal(view.calls.length, 1);
  assert.equal(view.find('Button').onClick, undefined);
  await view.find('Popconfirm').onConfirm();
  assert.equal(view.calls[1][0], '/api/os/update/apk/rollback');
});

test('custom repositories and active operations disable channel switching', () => {
  assert.equal(render({ channel: 'custom', available: false }).find('Select').disabled, true);
  for (const state of ['checking', 'installing', 'rebooting']) {
    const view = render({ channel: 'stable', available: true, rollbackAvailable: true }, state);
    assert.equal(view.find('Select').disabled, true);
    assert.equal(view.find('Button').disabled, true);
    assert.equal(view.find('Popconfirm').disabled, true);
  }
});

test('beta warns, stable without prerelease packages has no rollback control', () => {
  assert.ok(render({ channel: 'beta', available: true }).find('Alert'));
  assert.equal(render({ channel: 'stable', available: true }).find('Popconfirm'), undefined);
  assert.equal(render(undefined).find('Select'), undefined); // older backend during upgrade
});
