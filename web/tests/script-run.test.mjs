import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

// The Script menu with state kept between renders; Run and MenuItem are
// stubs whose props the test reads, as the popover and dialog would get them.
function harness() {
  const state = [];
  let slot = 0;
  let elements = [];
  const runs = [];
  const element = () => null;
  const Run = () => null;
  const MenuItem = () => null;
  const record = (type, props) => {
    elements.push({ type, props });
    return { type, props };
  };
  const modules = {
    react: {
      useState: (initial) => {
        const index = slot++;
        if (!(index in state)) state[index] = initial;
        return [
          state[index],
          (value) => {
            state[index] = typeof value === 'function' ? value(state[index]) : value;
          }
        ];
      },
      useRef: (value) => {
        const index = slot++;
        if (!(index in state)) state[index] = { current: value };
        return state[index];
      }
    },
    'react/jsx-runtime': { jsx: record, jsxs: record, Fragment: element },
    '@ant-design/icons': { UploadOutlined: element },
    antd: { Button: element, Divider: element, Popconfirm: element, Switch: element },
    clsx: (...names) => names.filter(Boolean).join(' '),
    'lucide-react': { ChevronRightIcon: element, FileJsonIcon: element, PowerIcon: element },
    'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
    '@/api/script.ts': {
      getScripts: async () => ({ code: 0, data: { files: ['a.sh'] } }),
      runScript: async (name, type) => {
        runs.push([name, type]);
        return { code: 0 };
      }
    },
    '@/lib/show-request-error.ts': { showRequestError: () => {} },
    '@/components/menu-item.tsx': { MenuItem },
    './run': { Run }
  };
  const code = ts.transpileModule(
    fs.readFileSync(new URL('../src/pages/desktop/menu/script/index.tsx', import.meta.url), 'utf8'),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, jsx: ts.JsxEmit.ReactJSX } }
  ).outputText;
  const exports = {};
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      assert.ok(name in modules, name);
      return modules[name];
    },
    console
  });
  const render = () => {
    slot = 0;
    elements = [];
    exports.Script();
    return elements;
  };
  const find = (predicate) => render().find(predicate)?.props;
  return {
    runs,
    menu: () => find((e) => e.type === MenuItem),
    run: () => find((e) => e.type === Run),
    row: () => find((e) => e.props?.className?.includes('space-x-5') && e.props.onClick),
    button: (label) => find((e) => e.props?.children === label && e.props.onClick)
  };
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 0));

test('closing the menu from inside the Run dialog keeps its script', async () => {
  const h = harness();
  h.menu().onOpenChange(true);
  await settle();
  h.row().onClick();
  h.button('script.run').onClick();
  assert.equal(h.run()?.script, 'a.sh');
  // A click in the dialog is outside the popover: the menu closes.
  h.menu().onOpenChange(false);
  assert.equal(h.run()?.script, 'a.sh', 'the dialog still shows the script it runs');
  assert.deepEqual(h.runs, [], 'a foreground run is started by the dialog, not the menu');
});
