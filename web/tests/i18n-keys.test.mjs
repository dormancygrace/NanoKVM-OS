import assert from 'node:assert/strict';
import { readdirSync } from 'node:fs';
import test from 'node:test';

import languages from '../src/i18n/languages.ts';

const localesDir = new URL('../src/i18n/locales/', import.meta.url);
const pluralSuffix = /_(zero|one|two|few|many|other)$/;

function flatten(object, prefix = '', out = new Map()) {
  for (const [key, value] of Object.entries(object)) {
    const path = prefix ? `${prefix}.${key}` : key;
    if (value && typeof value === 'object') flatten(value, path, out);
    else out.set(path, value);
  }
  return out;
}

const names = readdirSync(localesDir)
  .filter((file) => file.endsWith('.ts'))
  .map((file) => file.slice(0, -3))
  .sort();
const locales = new Map();
for (const name of names) {
  const module = await import(new URL(`${name}.ts`, localesDir));
  locales.set(name, flatten(module.default.translation));
}
const en = locales.get('en');
// Plural forms differ between languages: a locale may use any CLDR suffix
// for a key that English pluralizes, and any suffix satisfies it.
const base = (key) => key.replace(pluralSuffix, '');
const englishBases = new Set([...en.keys()].map(base));
const has = (keys, key) =>
  keys.has(key) || (pluralSuffix.test(key) && [...keys.keys()].some((k) => base(k) === base(key)));
const placeholders = (value) =>
  new Set([...String(value).matchAll(/\{\{\s*([\w.]+)[^}]*\}\}/g)].map((match) => match[1]));

test('the language list and locale files match', () => {
  assert.deepEqual(languages.map(({ key }) => key).sort(), names);
});

test('locales contain only keys that exist in en.ts', () => {
  const orphans = [];
  for (const [name, keys] of locales) {
    for (const key of keys.keys()) {
      if (!en.has(key) && !(pluralSuffix.test(key) && englishBases.has(base(key)))) {
        orphans.push(`${name}: ${key}`);
      }
    }
  }
  assert.deepEqual(orphans, [], 'remove these keys or add them to en.ts');
});

test('complete languages translate every key in en.ts', () => {
  const missing = [];
  for (const { key: name, partial } of languages) {
    if (partial) continue;
    const keys = locales.get(name);
    for (const key of en.keys()) if (!has(keys, key)) missing.push(`${name}: ${key}`);
  }
  assert.deepEqual(missing, [], 'translate these keys or mark the language as partial');
});

test('translations use only interpolation values that English provides', () => {
  const unknown = [];
  for (const [name, keys] of locales) {
    for (const [key, value] of keys) {
      const english = en.get(key) ?? en.get(`${base(key)}_other`) ?? en.get(base(key));
      if (english === undefined) continue;
      const allowed = placeholders(english);
      if (pluralSuffix.test(key) || en.has(`${base(key)}_other`)) allowed.add('count');
      for (const variable of placeholders(value)) {
        if (!allowed.has(variable)) unknown.push(`${name}: ${key} uses {{${variable}}}`);
      }
    }
  }
  assert.deepEqual(unknown, []);
});
