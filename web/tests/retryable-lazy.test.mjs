import assert from 'node:assert/strict';
import test from 'node:test';
import { createElement, lazy } from 'react';
import { renderToReadableStream } from 'react-dom/server';

import { retryableLazy } from '../src/lib/retryable-lazy.ts';

const render = async (Component, props) => {
  const stream = await renderToReadableStream(createElement(Component, props), { onError() {} });
  return new Response(stream).text();
};

test('a failed settings import can be explicitly retried without losing successful pages', async () => {
  let calls = 0;
  const page = retryableLazy(async () => {
    if (++calls === 1) throw new Error('Failed to fetch dynamically imported module');
    return { default: ({ name }) => createElement('div', null, name) };
  });
  await assert.rejects(render(page.Component, { name: 'Updates' }), /Failed to fetch/);
  await assert.rejects(render(page.Component, { name: 'Updates' }), /Failed to fetch/);
  assert.equal(calls, 1, 'failure stays visible until the user retries');
  page.retry();
  assert.equal(await render(page.Component, { name: 'Updates' }), '<div>Updates</div>');
  page.retry();
  assert.equal(await render(page.Component, { name: 'Software' }), '<div>Software</div>');
  assert.equal(calls, 2, 'successful import remains cached');
});

test('plain React.lazy reproduces the permanent failed import after a temporary outage', async () => {
  let online = false;
  let calls = 0;
  const Page = lazy(async () => {
    calls++;
    if (!online) throw new Error('offline');
    return { default: () => createElement('div', null, 'loaded') };
  });
  await assert.rejects(render(Page), /offline/);
  online = true;
  await assert.rejects(render(Page), /offline/);
  assert.equal(calls, 1);
});

test('persistent import failures do not trigger an automatic retry or reload loop', async () => {
  let calls = 0;
  const page = retryableLazy(async () => {
    calls++;
    throw new Error('old chunk missing');
  });
  for (let i = 0; i < 3; i++) {
    await assert.rejects(render(page.Component), /old chunk missing/);
    assert.equal(calls, i + 1);
    page.retry();
  }
});
