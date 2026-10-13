import i18n from 'i18next';
import type { ResourceLanguage } from 'i18next';
import { initReactI18next } from 'react-i18next';

import { getLanguage } from '@/lib/localstorage.ts';

import languages from './languages.ts';
import { createLocaleBackend, localeOptions, localeTag, selectLanguage } from './locale-loader.ts';
import en from './locales/en.json';

// English stays bundled as the fallback. Other languages are separate chunks,
// requested by i18next only when chosen. Each file holds the translation
// namespace itself (i18next JSON, the format Weblate edits).
const modules = import.meta.glob<ResourceLanguage[string]>(
  ['./locales/*.json', '!./locales/en.json'],
  { import: 'default' }
);
const loaders: Record<string, () => Promise<ResourceLanguage>> = {};
for (const path in modules) {
  const name = path.split('/').pop()?.replace('.json', '');
  if (name)
    loaders[localeTag(name)] = () => modules[path]().then((translation) => ({ translation }));
}

// Wait for the selected locale before rendering the application.
export const i18nReady = i18n
  .use(createLocaleBackend(loaders))
  .use(initReactI18next)
  .init({
    resources: { en: { translation: en } },
    ...localeOptions,
    lng: selectLanguage(
      getLanguage(),
      navigator.language,
      languages.map(({ key }) => key)
    ),
    interpolation: {
      escapeValue: false
    }
  });

export default i18n;
