import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import test from 'node:test';

// Checks the production chunk graph produced by the codeSplitting groups in
// vite.config.ts. Build the web app before running it.
const dist = new URL('../dist/', import.meta.url);
const assets = new URL('assets/', dist);
const names = readdirSync(assets).filter((name) => name.endsWith('.js'));
assert.ok(names.length > 0, 'Build the web app before running the chunk tests');

// Static `import … from "./x.js"`, `import "./x.js"` and `export … from "./x.js"`;
// dynamic `import("./x.js")` is not matched.
const staticImport = /\b(?:import|export)\s*(?:[\w${}\s,*]*?\bfrom\s*)?["']\.\/([^"']+\.js)["']/g;
const imports = new Map(
  names.map((name) => {
    const code = readFileSync(new URL(name, assets), 'utf8');
    return [name, [...code.matchAll(staticImport)].map((match) => match[1])];
  })
);

function closure(roots) {
  const seen = new Set();
  const stack = [...roots];
  while (stack.length) {
    const name = stack.pop();
    if (seen.has(name)) continue;
    seen.add(name);
    stack.push(...(imports.get(name) ?? []));
  }
  return seen;
}

const find = (pattern) => {
  const name = names.find((candidate) => pattern.test(candidate));
  assert.ok(name, `missing chunk ${pattern}`);
  return name;
};
const entry = readFileSync(new URL('index.html', dist), 'utf8').match(
  /<script type="module"[^>]*src="\/assets\/([^"]+\.js)"/
)[1];
// Code of large optional parts, recognised by content rather than file name.
const optional = {
  'terminal (xterm)': 'xterm-helper-textarea',
  'virtual keyboard': 'hg-theme-default',
  'picoclaw (markdown)': 'gfmFootnote',
  'Russian locale': 'Выделение видеопамяти'
};

test('static imports between chunks have no cycles', () => {
  const state = new Map();
  const visit = (name, path) => {
    state.set(name, 'active');
    for (const dependency of imports.get(name) ?? []) {
      assert.notEqual(
        state.get(dependency),
        'active',
        `cycle: ${[...path, name, dependency].join(' -> ')}`
      );
      if (!state.has(dependency)) visit(dependency, [...path, name]);
    }
    state.set(name, 'done');
  };
  for (const name of names) if (!state.has(name)) visit(name, []);
});

test('the login and desktop pages load optional parts lazily', () => {
  const initial = closure([
    entry,
    find(/^login-.*\.js$/),
    find(/^desktop-.*\.js$/),
    find(/^admin-items-.*\.js$/),
    find(/^h264-direct-.*\.js$/)
  ]);
  const code = new Map(names.map((name) => [name, readFileSync(new URL(name, assets), 'utf8')]));
  for (const [part, marker] of Object.entries(optional)) {
    assert.ok(
      names.some((name) => code.get(name).includes(marker)),
      `${part} not found`
    );
    for (const name of initial)
      assert.ok(!code.get(name).includes(marker), `${name} contains ${part}`);
  }
  // The players other than the default one stay separate chunks.
  for (const player of [/^h264-webrtc-.*\.js$/, /^mjpeg-.*\.js$/]) {
    assert.ok(!initial.has(find(player)), `${find(player)} is loaded statically`);
  }
  assert.ok(names.some((name) => /^direct\.worker-.*\.js$/.test(name)));
});
