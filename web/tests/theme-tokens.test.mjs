import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

import { themeTokens } from '../src/lib/theme-tokens.ts';

test('Tailwind @theme colours equal the antd theme tokens', () => {
  const css = readFileSync(new URL('../src/assets/styles/index.css', import.meta.url), 'utf8');
  const block = css.match(/@theme \{([^}]*)\}/)[1];
  const vars = Object.fromEntries(
    [...block.matchAll(/--color-([a-z-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()])
  );
  const camel = (name) => name.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
  assert.deepEqual(Object.fromEntries(Object.entries(vars).map(([k, v]) => [camel(k), v])), {
    ...themeTokens
  });
});
