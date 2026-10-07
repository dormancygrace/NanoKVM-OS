# How to add a language

1. Add a language file in i18n/locales folder (for example: en.ts).
2. Add language key and name in i18n/languages.ts (for example: { key: 'en', name: 'English' }).
3. Add `partial: true` while the file covers less than about 90% of the keys in `en.ts`.
   `tests/i18n-keys.test.mjs` fails if a locale has keys that are not in `en.ts`, or if a
   locale without the flag misses any key.
