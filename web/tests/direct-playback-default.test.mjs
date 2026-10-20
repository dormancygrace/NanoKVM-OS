import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

function loadStorage(stored) {
  const source = ts.transpileModule(
    readFileSync(new URL('../src/lib/localstorage.ts', import.meta.url), 'utf8'),
    { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }
  ).outputText;
  const items = new Map(Object.entries(stored));
  const localStorage = {
    getItem: (key) => (items.has(key) ? items.get(key) : null),
    setItem: (key, value) => items.set(key, String(value))
  };
  const exports = {};
  vm.runInNewContext(source, { exports, require: () => ({}), localStorage });
  return exports;
}

test('Direct playback defaults to lowest latency; a saved choice stays', () => {
  assert.equal(loadStorage({}).getDirectPlayback(), 'immediate');
  assert.equal(loadStorage({ 'direct-playback': 'immediate' }).getDirectPlayback(), 'immediate');
  assert.equal(loadStorage({ 'direct-playback': 'paced' }).getDirectPlayback(), 'paced');
  assert.equal(loadStorage({ 'direct-playback': 'unknown' }).getDirectPlayback(), 'immediate');
});
