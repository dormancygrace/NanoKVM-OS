import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import test from 'node:test';
import { gzipSync } from 'node:zlib';

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
  'Russian locale': 'Сначала используется ZRAM'
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
    find(/^root-.*\.js$/),
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

// Sum each reachable file once, rather than moving bytes between named chunks.
// Includes the eagerly used admin menu/default player and its worker on desktop.
// Limits retain modest headroom; changes must report both bytes and requests.
test('startup JavaScript stays within gzip and request budgets', () => {
  const routes = {
    entry: { roots: [entry], bytes: 250_000, requests: 3 },
    login: { roots: [entry, find(/^login-.*\.js$/)], bytes: 390_000, requests: 15 },
    desktop: {
      roots: [
        entry,
        find(/^root-.*\.js$/),
        find(/^desktop-.*\.js$/),
        find(/^admin-items-.*\.js$/),
        find(/^h264-direct-.*\.js$/),
        find(/^direct\.worker-.*\.js$/)
      ],
      bytes: 625_000,
      requests: 50
    }
  };
  for (const [route, budget] of Object.entries(routes)) {
    const files = closure(budget.roots);
    const bytes = [...files].reduce(
      (total, name) => total + gzipSync(readFileSync(new URL(name, assets)), { level: 9 }).length,
      0
    );
    assert.ok(bytes <= budget.bytes, `${route}: ${bytes} gzip bytes > ${budget.bytes}`);
    assert.ok(
      files.size <= budget.requests,
      `${route}: ${files.size} requests > ${budget.requests}`
    );
    console.info(`${route}: ${bytes} gzip bytes, ${files.size} JavaScript requests`);
  }
  const login = closure(routes.login.roots);
  assert.ok(!login.has(find(/^tips-dialog-.*\.js$/)), 'password help must load only when opened');
  assert.ok(!login.has(find(/^root-.*\.js$/)), 'authenticated shell must stay out of login');
});
