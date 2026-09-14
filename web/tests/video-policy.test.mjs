import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import vm from 'node:vm';
import ts from 'typescript';

const source = readFileSync(new URL('../src/lib/video-policy.ts', import.meta.url), 'utf8')
  .replace(/^import .*;\n/gm, '')
  .replace(/^export /gm, '');
const code = ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 }
}).outputText;

function policy() {
  const sandbox = { exports: {} };
  vm.runInNewContext(`${code}\nexports.isQhdStream = isQhdStream;`, sandbox);
  return sandbox.exports.isQhdStream;
}

test('the tested 1088×1920 same-as-input portrait stream is FHD-class', () => {
  assert.equal(policy()(0, 1088, 1920), false);
});

test('standard portrait inputs are allowed while QHD stream limits remain blocked', () => {
  const isQhdStream = policy();
  assert.equal(isQhdStream(0, 1080, 1920), false);
  assert.equal(isQhdStream(0, 720, 1280), false);
  assert.equal(isQhdStream(0, 1200, 1920), true);
  assert.equal(isQhdStream(0, 1920, 1088), true);
  assert.equal(isQhdStream(0, 1440, 2560), true);
  assert.equal(isQhdStream(1440, 1088, 1920), true);
});

test('the exact portrait input does not change ordinary FHD limits', () => {
  const isQhdStream = policy();
  assert.equal(isQhdStream(1080, 1088, 1920), false);
  assert.equal(isQhdStream(720, 2560, 1440), false);
});

test('maximum profile blocks incompatible transports and codecs, including timing transitions', async () => {
  for (const data of [
    { portrait: true, portraitResolution: 2560, inputWidth: 1088, inputHeight: 1920 },
    { portrait: false, inputWidth: 1440, inputHeight: 2560 }
  ]) {
    const sandbox = { exports: {}, getScreen: async () => ({ code: 0, data }) };
    vm.runInNewContext(code + '\nexports.guard = isMaximumPortraitBlocked;', sandbox);
    assert.equal(await sandbox.exports.guard('direct', 'h265'), false);
    assert.equal(await sandbox.exports.guard('direct', 'h264'), true);
    assert.equal(await sandbox.exports.guard('h264', 'h265'), true);
    assert.equal(await sandbox.exports.guard('mjpeg', 'h265'), true);
  }
  const sandbox = {
    exports: {},
    getScreen: async () => ({
      code: 0,
      data: { portrait: true, portraitResolution: 1920, inputWidth: 1088, inputHeight: 1920 }
    })
  };
  vm.runInNewContext(code + '\nexports.guard = isMaximumPortraitBlocked;', sandbox);
  assert.equal(await sandbox.exports.guard('h264', 'h265'), false);
});
