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

test('preloading shares one in-flight import with rendering and caches success', async () => {
  let calls = 0;
  let release;
  const gate = new Promise((resolve) => {
    release = resolve;
  });
  const page = retryableLazy(async () => {
    calls++;
    await gate;
    return { default: () => createElement('div', null, 'ready') };
  });
  assert.equal(calls, 0, 'creating a lazy page must not start its import');
  const first = page.preload();
  const second = page.preload();
  const view = render(page.Component);
  await new Promise(setImmediate);
  assert.equal(calls, 1, 'hover, focus and opening share one pending import');
  page.retry(); // A pending or successful import must keep its identity.
  release();
  await Promise.all([first, second]);
  assert.equal(await view, '<div>ready</div>');
  await page.preload();
  assert.equal(await render(page.Component), '<div>ready</div>');
  assert.equal(calls, 1);
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

test('a preload failing after opening preserves the boundary and explicit retry', async () => {
  let calls = 0;
  let fail;
  const gate = new Promise((resolve, reject) => {
    fail = reject;
  });
  const page = retryableLazy(async () => {
    if (++calls === 1) await gate;
    return { default: () => createElement('div', null, 'recovered') };
  });
  const preload = page.preload();
  const failure = assert.rejects(render(page.Component), /offline/);
  await new Promise(setImmediate);
  fail(new Error('offline'));
  await Promise.all([preload, failure]);
  await assert.rejects(render(page.Component), /offline/);
  assert.equal(calls, 1, 'an open panel keeps the failure until explicit retry');
  page.retry();
  assert.equal(await render(page.Component), '<div>recovered</div>');
  assert.equal(calls, 2);
});

test('a synchronous loader failure is handled by preload and the local boundary', async () => {
  const page = retryableLazy(() => {
    throw new Error('loader failed');
  });
  await page.preload();
  await assert.rejects(render(page.Component), /loader failed/);
});
