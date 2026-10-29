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
      if (name.startsWith('@/lib/http')) return { http: { post: (...args) => calls.push(args) } };
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
