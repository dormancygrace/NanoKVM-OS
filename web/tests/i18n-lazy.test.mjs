import assert from 'node:assert/strict';
import test from 'node:test';
import { createInstance } from 'i18next';

import {
  changeLoadedLanguage,
  createLocaleBackend,
  localeTag,
  selectLanguage
} from '../src/i18n/locale-loader.ts';

const english = { translation: { greeting: 'Hello', fallback: 'English fallback' } };

function initialize(loaders, language = 'en') {
  const instance = createInstance().use(createLocaleBackend(loaders));
  const ready = instance.init({
    resources: { en: english },
    partialBundledLanguages: true,
    lng: language,
    fallbackLng: 'en',
    initAsync: false
  });
  return { instance, ready };
}

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

test('English is available without requesting any locale chunk', async () => {
  let calls = 0;
  const { instance, ready } = initialize({
    ru: async () => {
      calls++;
      return { translation: { greeting: 'Привет' } };
    }
  });
  await ready;
  assert.equal(calls, 0);
  assert.equal(instance.t('greeting'), 'Hello');
});

test('initialization waits for the selected chunk and does not load unrelated locales', async () => {
  const calls = [];
  let release;
  const selected = new Promise((resolve) => {
    release = resolve;
  });
  const { instance, ready } = initialize(
    {
      ru: () => {
        calls.push('ru');
        return selected;
      },
      fr: async () => {
        calls.push('fr');
        return { translation: { greeting: 'Bonjour' } };
      }
    },
    'ru'
  );
  let initialized = false;
  void ready.then(() => {
    initialized = true;
  });
  await Promise.resolve();
  assert.equal(initialized, false);
  assert.deepEqual(calls, ['ru']);
  release({ translation: { greeting: 'Привет' } });
  await ready;
  assert.equal(instance.t('greeting'), 'Привет');
  assert.equal(instance.t('fallback'), 'English fallback');
});

test('language changes keep the previous translations until the new chunk is ready', async () => {
  let release;
  let loads = 0;
  const { instance, ready } = initialize({
    fr: () => {
      loads++;
      return new Promise((resolve) => {
        release = resolve;
      });
    }
  });
  await ready;
  const changing = changeLoadedLanguage(instance, 'fr');
  assert.equal(instance.language, 'en');
  assert.equal(instance.t('greeting'), 'Hello');
  release({ translation: { greeting: 'Bonjour' } });
  await changing;
  assert.equal(instance.language, 'fr');
  assert.equal(instance.t('greeting'), 'Bonjour');

  await changeLoadedLanguage(instance, 'en');
  await changeLoadedLanguage(instance, 'fr');
  assert.equal(loads, 1);
});

test('failed language chunks leave the active language intact, including repeated selection', async () => {
  const { instance, ready } = initialize({
    ru: async () => {
      throw new Error('chunk unavailable');
    }
  });
  await ready;
  await assert.rejects(changeLoadedLanguage(instance, 'ru'));
  assert.equal(instance.language, 'en');
  assert.equal(instance.t('greeting'), 'Hello');
  await assert.rejects(changeLoadedLanguage(instance, 'ru'));
  assert.equal(instance.language, 'en');
  assert.equal(instance.t('greeting'), 'Hello');
});

test('a failed initial locale still renders bundled English instead of raw translation keys', async () => {
  const { instance, ready } = initialize(
    {
      ru: async () => {
        throw new Error('offline');
      }
    },
    'ru'
  );
  await ready;
  assert.equal(instance.t('greeting'), 'Hello');
  assert.equal(instance.t('fallback'), 'English fallback');
});

test('canonical Czech locale tag preserves the few plural form', async () => {
  const { instance, ready } = initialize(
    {
      cs: async () => ({
        translation: {
          count_one: 'one',
          count_few: 'few',
          count_other: 'other'
        }
      })
    },
    localeTag('cz')
  );
  await ready;
  assert.equal(instance.t('count', { count: 1 }), 'one');
  assert.equal(instance.t('count', { count: 2 }), 'few');
  assert.equal(instance.t('count', { count: 5 }), 'other');
});
