import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import test from 'node:test';
import { getResponse } from 'msw/utils/get-response';

import { handlers } from '../src/mocks/handlers.ts';

const require = createRequire(import.meta.url);

test('checked-in service worker matches the installed MSW protocol', async () => {
  const actual = await readFile(new URL('../msw/mockServiceWorker.js', import.meta.url), 'utf8');
  const expected = await readFile(require.resolve('msw/mockServiceWorker.js'), 'utf8');
  assert.equal(actual, expected);
});

test('mocked login, logout and branding work with the installed MSW', async () => {
  // Resolve relative browser routes against the same explicit origin in Node.
  const call = async (path, init) => {
    const response = await getResponse(handlers, new Request(`http://localhost${path}`, init), {
      baseUrl: 'http://localhost'
    });
    assert.ok(response, `No mock response for ${path}`);
    return response;
  };
  assert.equal((await call('/api/auth/account')).status, 401);
  assert.equal((await (await call('/api/auth/login', { method: 'POST' })).json()).code, 0);
  assert.equal((await (await call('/api/auth/account')).json()).data.username, 'admin');
  const updated = await call('/api/branding/banner-style', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ style: 'rainbow' })
  });
  assert.equal((await updated.json()).data.bannerStyle, 'rainbow');
  assert.equal((await (await call('/api/branding')).json()).data.bannerStyle, 'rainbow');
  const rejected = await call('/api/branding/banner-style', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ style: 'invalid' })
  });
  assert.equal((await rejected.json()).code, -1);
  await call('/api/auth/logout', { method: 'POST' });
  assert.equal((await call('/api/auth/account')).status, 401);
});
