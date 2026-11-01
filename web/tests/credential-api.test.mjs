import assert from 'node:assert/strict';
import fs from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

function loadApi(name, calls) {
  const code = ts.transpileModule(
    fs.readFileSync(new URL(`../src/api/${name}.ts`, import.meta.url), 'utf8'),
    {
      compilerOptions: { module: ts.ModuleKind.CommonJS }
    }
  ).outputText;
  const exports = {};
  vm.runInNewContext(code, {
    exports,
    require: (name) => {
      if (name.startsWith('@/lib/http'))
        return {
          http: {
            post: (...args) => calls.push(args),
            request: (config) => calls.push([config.url, config.data, config])
          }
        };
      if (name === '@/lib/input-lease.ts') return {};
      throw new Error(name);
    }
  });
  return exports;
}

test('every password request explicitly preserves the plaintext and suppresses credential diagnostics', () => {
  const calls = [];
  const auth = loadApi('auth', calls);
  const device = loadApi('vm', calls);
  for (const password of ['päss+%20密碼', 'U2FsdGVkX1-not-ciphertext', ' white space ']) {
    auth.login('admin', password);
    auth.changePassword(password, password);
    auth.createUser('alice', password, 'user');
    auth.resetUserPassword('alice', password);
    device.enableSSH(password);
    for (const [url, body, config] of calls.splice(0)) {
      assert.equal(body.password, password, url);
      assert.equal(body.passwordEncoding, 'plain', url);
      if (url === '/api/auth/password') assert.equal(body.currentPassword, password);
      assert.equal(config.sensitive, true, url);
    }
  }
});

test('the BMC password write is marked sensitive', () => {
  const calls = [];
  loadApi('bmc', calls).configureBmc({ username: 'admin', password: 'fixture-secret' });
  assert.equal(calls[0][2].sensitive, true);
});

// The HTTP error interceptor must not print requests that carry credentials,
// and a 401 must reach the login redirect.
test('failed sensitive requests stay out of the console; 401 signals expiry', async () => {
  const source = fs
    .readFileSync(new URL('../src/lib/http.ts', import.meta.url), 'utf8')
    .replace('import.meta.env', '({})');
  const code = ts.transpileModule(source, {
    compilerOptions: { module: ts.ModuleKind.CommonJS }
  }).outputText;
  let onError;
  const logs = [];
  let expired = 0;
  vm.runInNewContext(code, {
    exports: {},
    console: { log: (...args) => logs.push(args) },
    require: (name) => {
      if (name === 'axios')
        return {
          __esModule: true,
          default: {
            create: () => ({
              interceptors: {
                request: { use() {} },
                response: {
                  use(_ok, fail) {
                    onError = fail;
                  }
                }
              }
            })
          }
        };
      if (name.includes('auth-events'))
        return { notifyAuthExpired: () => expired++, notifyPasswordChangeRequired() {} };
      if (name.includes('request-error'))
        return { isHandledRequestError: () => false, passwordChangeRequiredCode: -10 };
      if (name.includes('service')) return { getBaseUrl: () => '' };
      throw new Error(name);
    }
  });
  const fail = (config, status) =>
    onError({ config, response: { status, data: {} } }).catch(() => {});
  await fail({ sensitive: true }, 500);
  assert.deepEqual(logs, []);
  await fail({}, 500);
  assert.equal(logs.length, 1);
  await fail({ sensitive: true }, 401);
  assert.equal(expired, 1);
});
