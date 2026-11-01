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

test('failed speculative preloads do not poison the first open after recovery', async () => {
  let calls = 0;
  let online = false;
  const page = retryableLazy(async () => {
    calls++;
    if (!online) throw new Error('offline');
    return { default: () => createElement('div', null, 'recovered') };
  });
  await page.preload();
  assert.equal(calls, 1);
  await new Promise(setImmediate);
  assert.equal(calls, 1, 'there is no automatic retry loop');
  await page.preload();
  assert.equal(calls, 2, 'a later user interaction may make another attempt');
  online = true;
  assert.equal(await render(page.Component), '<div>recovered</div>');
  assert.equal(calls, 3, 'first open retries a failed speculative request');
});
