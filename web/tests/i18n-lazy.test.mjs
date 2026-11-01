import assert from 'node:assert/strict';
import test from 'node:test';

import { selectLanguage } from '../src/i18n/locale-loader.ts';

test('saved legacy locale names and browser regional tags select the same supported language', () => {
  const supported = ['en', 'ru', 'pt_br', 'zh_tw', 'zh', 'cz', 'se'];
  assert.equal(selectLanguage('pt_br', 'ru-RU', supported), 'pt-BR');
  assert.equal(selectLanguage('zh_tw', 'en-US', supported), 'zh-TW');
  assert.equal(selectLanguage(null, 'pt-BR', supported), 'pt-BR');
  assert.equal(selectLanguage(undefined, 'zh-TW', supported), 'zh-TW');
  assert.equal(selectLanguage(undefined, 'cs-CZ', supported), 'cs');
  assert.equal(selectLanguage(undefined, 'sv-SE', supported), 'sv');
  assert.equal(selectLanguage('unsupported', 'ru-RU', supported), 'ru');
  assert.equal(selectLanguage(undefined, 'unknown', supported), 'en');
});
