import assert from 'node:assert/strict';
import test from 'node:test';

import { modelConfigComplete, modelProviderAllowsEmptyApiKey } from '../src/lib/picoclaw-model.ts';

test('local provider model forms accept no key but still require a base and model', () => {
  for (const provider of ['ollama', 'lmstudio', 'vllm']) {
    const model = provider + '/example/model';
    assert.equal(modelConfigComplete(model, 'http://localhost:11434/v1', ''), true);
    assert.equal(modelConfigComplete(model, '', ''), false);
    assert.equal(modelConfigComplete(model, '   ', ''), false);
    assert.equal(modelConfigComplete(model, 'http://localhost:11434/v1', 'explicit-key'), true);
  }
  assert.equal(modelConfigComplete('', 'http://localhost:11434', ''), false);
});

test('provider recognition follows backend trimming and case rules', () => {
  assert.equal(modelProviderAllowsEmptyApiKey('  OLLAMA /example  '), true);
  assert.equal(modelProviderAllowsEmptyApiKey('LMSTUDIO/example'), true);
  assert.equal(modelProviderAllowsEmptyApiKey('vLlM/example'), true);
});

test('hosted and generic compatible providers retain required keys', () => {
  for (const model of [
    'openai/gpt',
    'openai_compatible/model',
    'anthropic/claude',
    'ollama',
    '/ollama',
    'notollama/model',
    'openai/ollama'
  ]) {
    assert.equal(modelProviderAllowsEmptyApiKey(model), false);
    assert.equal(modelConfigComplete(model, 'http://localhost:11434/v1', ''), false);
    assert.equal(modelConfigComplete(model, 'http://localhost:11434/v1', 'key'), true);
  }
});
