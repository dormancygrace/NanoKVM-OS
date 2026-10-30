import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { brotliDecompressSync, gunzipSync } from 'node:zlib';

import { precompressAssets } from '../scripts/precompress.mjs';

function fixture(t) {
  const root = mkdtempSync(join(tmpdir(), 'nanokvm-precompress-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return root;
}

test('build assets have deterministic gzip and Brotli variants containing the original bytes', (t) => {
  const root = fixture(t);
  const data = Buffer.from('const message = "NanoKVM compressed asset";\n'.repeat(100));
  for (const extension of ['js', 'css', 'svg', 'json', 'wasm'])
    writeFileSync(join(root, `app.${extension}`), data);
  const totals = precompressAssets(root);
  assert.equal(totals.gz.files, 5);
  assert.equal(totals.br.files, 5);
  const before = new Map();
  for (const extension of ['js', 'css', 'svg', 'json', 'wasm']) {
    for (const [suffix, decode] of [
      ['gz', gunzipSync],
      ['br', brotliDecompressSync]
    ]) {
      const file = join(root, `app.${extension}.${suffix}`);
      const compressed = readFileSync(file);
      assert.deepEqual(decode(compressed), data);
      assert.ok(compressed.length <= data.length * 0.9);
      before.set(file, compressed);
    }
  }
  assert.deepEqual(precompressAssets(root), totals);
  for (const [file, data] of before) assert.deepEqual(readFileSync(file), data);
});

test('small, incompressible and unsupported files get no variants and stale variants are removed', (t) => {
  const root = fixture(t);
  const content = {
    'small.js': 'small',
    'random.js': randomBytes(4096),
    'image.png': 'x'.repeat(4096)
  };
  for (const [name, data] of Object.entries(content)) writeFileSync(join(root, name), data);
  mkdirSync(join(root, 'directory.js'));
  for (const suffix of ['gz', 'br']) writeFileSync(join(root, `small.js.${suffix}`), 'obsolete');
  const totals = precompressAssets(root);
  assert.equal(totals.gz.files, 0);
  assert.equal(totals.br.files, 0);
  for (const name of Object.keys(content))
    for (const suffix of ['gz', 'br'])
      assert.equal(existsSync(join(root, `${name}.${suffix}`)), false);
});
