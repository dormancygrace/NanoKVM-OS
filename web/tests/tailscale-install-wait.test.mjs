import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

// The Tailscale install panel with a stateful render; the API is a stub and
// the 3 s status poll runs at once.
function harness({ install, statuses }) {
  const state = [];
  const cleanups = [];
  let slot = 0;
  const calls = { success: 0, locked: [], status: 0 };
  let rendered;
  const element = () => null;
  const ExtensionInstallResult = () => null;
  const Result = () => null;
  const record = (type, props) => {
    if (type === ExtensionInstallResult || type === Result) rendered = { type, props };
    return { type, props };
  };
  const modules = {
    react: {
      useState: (initial) => {
        const index = slot++;
        if (!(index in state)) state[index] = initial;
        return [state[index], (value) => (state[index] = value)];
      },
      useRef: (value) => {
        const index = slot++;
        if (!(index in state)) state[index] = { current: value };
        return state[index];
      },
      useEffect: (fn) => {
        const index = slot++;
        if (!(index in state)) {
          state[index] = true;
          const cleanup = fn();
          if (cleanup) cleanups.push(cleanup);
        }
      }
    },
    'react/jsx-runtime': { jsx: record, jsxs: record, Fragment: element },
    antd: { Button: element, Result },
    axios: { isAxiosError: (err) => err?.isAxiosError === true },
    'lucide-react': { CircleAlertIcon: element },
    'react-i18next': { useTranslation: () => ({ t: (key) => key }) },
    '@/api/extensions/tailscale.ts': {
      install,
      getStatus: async () => {
        calls.status++;
        return { code: 0, data: statuses.shift() ?? { state: 'notInstall' } };
      }
    },
    '../vpn/extension-install-result.tsx': { ExtensionInstallResult }
  };
  const code = ts.transpileModule(
    fs.readFileSync(
      new URL('../src/pages/desktop/menu/settings/tailscale/install.tsx', import.meta.url),
      'utf8'
    ),
    {
      compilerOptions: {
        module: ts.ModuleKind.CommonJS,
        jsx: ts.JsxEmit.ReactJSX,
        target: ts.ScriptTarget.ES2022
      }
    }
  ).outputText;
  const exports = {};
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      assert.ok(name in modules, name);
      return modules[name];
    },
    setTimeout: (fn) => setImmediate(fn),
    Date,
    Promise,
    console
  });
  const render = () => {
    slot = 0;
    exports.Install({
      setIsLocked: (locked) => calls.locked.push(locked),
      onSuccess: () => calls.success++
    });
    return rendered;
  };
  return { calls, render, state, unmount: () => cleanups.forEach((cleanup) => cleanup()) };
}

const settle = () => new Promise((resolve) => setTimeout(resolve, 50));
const timeout = () =>
  Promise.reject({ isAxiosError: true, response: undefined, code: 'ECONNABORTED' });

test('an install that outlives the request is waited for, not reported failed', async () => {
  const h = harness({
    install: timeout,
    statuses: [
      { state: 'notInstall', busy: true },
      { state: 'notInstall', busy: true },
      { state: 'notRunning' }
    ]
  });
  h.render().props.onInstall();
  await settle();
  assert.equal(h.calls.success, 1);
  assert.equal(h.calls.status, 3);
  assert.notEqual(h.render().type.name, 'Result');
  assert.deepEqual(h.calls.locked, [true, false], 'locked for the whole wait');
});

test('an install that ended without Tailscale is a failure', async () => {
  const h = harness({
    install: timeout,
    statuses: [{ state: 'notInstall', busy: true }, { state: 'notInstall' }]
  });
  h.render().props.onInstall();
  await settle();
  assert.equal(h.calls.success, 0);
  assert.equal(h.state[0], 'failed');
});

test('an answer from the server is taken as it is', async () => {
  const h = harness({ install: async () => ({ code: -1, msg: 'apk failed' }), statuses: [] });
  h.render().props.onInstall();
  await settle();
  assert.equal(h.state[0], 'failed');
  assert.equal(h.calls.status, 0, 'no wait after a real answer');
});

test('closing the panel ends the wait', async () => {
  const statuses = Array.from({ length: 50 }, () => ({ state: 'notInstall', busy: true }));
  const h = harness({ install: timeout, statuses });
  h.render().props.onInstall();
  await new Promise((resolve) => setTimeout(resolve, 5));
  h.unmount();
  const polled = h.calls.status;
  await settle();
  assert.ok(h.calls.status <= polled + 1, `kept polling: ${polled} then ${h.calls.status}`);
  assert.equal(h.calls.success, 0);
  assert.notEqual(h.state[0], 'failed', 'no state written after unmount');
});
