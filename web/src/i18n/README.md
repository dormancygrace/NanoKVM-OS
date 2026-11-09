# Translations

Translations are maintained on Hosted Weblate; see [TRANSLATING.md](../../../TRANSLATING.md).

- `locales/<language>.json`: one i18next JSON file per language, holding the
  `translation` namespace. `en.json` is the source: new strings are added
  there, in code changes.
- `languages.ts`: the language menu. `partial: true` marks a language that
  translates well under 90 % of the keys in `en.json`; the rest falls back to
  English.
- `weblate.ts`: the "Help translate" link for each language.

To add a language, start it on Weblate or follow "Without Weblate" in
TRANSLATING.md, then add it to `languages.ts` with `partial: true`.
