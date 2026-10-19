# Translating NanoKVM OS

The web interface is translated on Hosted Weblate:
**https://hosted.weblate.org/engage/nanokvm-os/**

You don't need git or any knowledge of the code. Every language setting page in
the interface also has a **Help translate** link that opens your language there.

## For translators

1. Open the project, pick your language, and translate or fix strings. With a
   Weblate account your changes are saved directly. Without one you can still
   leave suggestions, and trusted translators approve them.
2. Weblate collects the changes and opens a pull request on GitHub. Our CI
   checks it like any other change.

Keep these unchanged:

- **`{{placeholders}}`**. You may move them within the sentence but must not
  rename them: `{{count}} cores` → `{{count}} Kerne`. Weblate flags a missing
  or renamed one.
- **Tags** such as `<ID>`, **line breaks**, and **leading or trailing spaces**.
- **Product and protocol names**: NanoKVM, RustDesk, Tailscale, NetBird,
  WireGuard, OpenVPN, HDMI, EDID, USB, H.264, H.265, MJPEG, WebRTC and others.
- **Units, paths and commands**: `fps`, `Mbit/s`, `MiB`, `/dev/ttyGS0`.

Use the terms that the language already uses for the same thing. Weblate shows
them in the glossary and in the "Other occurrences" and "Machinery" panels.

### Plural forms

The message counter, `picoclaw.history.messageCount`, counts whole messages.
Its English forms are `messageCount_one` and `messageCount_other`. Weblate
uses the forms needed for whole numbers in each language: for example,
`one/few/many` for Russian, `one/few/other` for Czech, and only `other`
for Japanese. Translate every form shown in Weblate.

For this integer counter, do not add fraction-only forms such as Russian
`messageCount_other` or Czech `messageCount_many`. The plural test checks
that the files contain exactly the whole-number forms used by each language.

## Complete and partial languages

`web/src/i18n/languages.ts` marks a language `partial: true` while it
translates well under 90 % of the English keys. The language menu shows it as
partial, and missing or empty strings fall back to English.

A language without the flag must provide a non-empty translation for every key.
`tests/i18n-keys.test.mjs`
fails otherwise. When a partial language reaches 100 % on Weblate, a
maintainer removes its flag. From then on CI keeps it complete: a new English
string must be translated before the change can merge.

## Without Weblate

Each language is one i18next JSON file: `web/src/i18n/locales/<language>.json`.
You can edit it in a pull request; run `pnpm test:i18n` in `web/` before
submitting. Weblate picks up such changes from the repository.

To add a new language without Weblate:

1. Copy the keys you translate from `en.json` to a new
   `locales/<language>.json`.
2. Add `{ key: '<language>', name: '<native name>', partial: true }` to
   `languages.ts`.

## Maintainers: Weblate setup

The project and component are set up once on hosted.weblate.org (libre
hosting for open-source projects).

**Project**

| Setting  | Value        |
| -------- | ------------ |
| Name     | NanoKVM OS   |
| URL slug | `nanokvm-os` |

**Component**

| Setting                        | Value                                                                                                        |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------ |
| Name                           | Web interface                                                                                                |
| URL slug                       | `web`                                                                                                        |
| Source code repository         | `https://github.com/dormancygrace/NanoKVM-OS.git`                                                            |
| Repository branch              | `main`                                                                                                       |
| Version control system         | GitHub App (the Weblate app, installed on this repository only, receives pushes and opens the pull requests) |
| File format                    | i18next JSON file v4                                                                                         |
| File mask                      | `web/src/i18n/locales/*.json`                                                                                |
| Monolingual base language file | `web/src/i18n/locales/en.json`                                                                               |
| Template for new translations  | Leave empty                                                                                                  |
| Source language                | English                                                                                                      |
| Edit base file                 | off (English strings change only with the code)                                                              |
| JSON indentation               | 2 spaces                                                                                                     |
| Sort JSON keys                 | off (Prettier formats these files the same way)                                                              |
| Language code style            | POSIX style using underscore as a separator, lower case                                                      |
| Add-ons                        | Cleanup translation files (removes keys that `en.json` no longer has)                                        |

Leave the template field empty for i18next JSON so new translations contain only
translated keys. The monolingual base file above still supplies English source
strings to Weblate. The interface also falls back to English for empty values
that arrive from existing translation files.

**Language aliases** (project settings). Locale file names predate Weblate
and are kept, because the interface stores the chosen language by these names:

```
cz:cs, se:sv, zh:zh_Hans, zh_tw:zh_Hant, pt_br:pt_BR
```

The links in the interface use the Weblate language codes:
`web/src/i18n/weblate.ts`. If the project or component slug differs from
the values above, update the URLs there.

When a new language starts on Weblate, it creates
`locales/<language>.json`. The language appears in the menu only after a
maintainer adds it to `languages.ts`, with `partial: true` while it is
incomplete.

## Maintainers: preventing synchronization conflicts

Before editing locale files or merging a PR that changes them, check the
[Weblate component](https://hosted.weblate.org/projects/nanokvm-os/web/) and
integrate its outstanding translation commits into the current upstream branch.
For broad key removals or locale restructuring, commit pending Weblate changes
and temporarily lock the component in **Operations → Repository maintenance**;
finish the merge, synchronize Weblate, then unlock it.

- Keep existing human translations and JSON key order. Limit code-side locale
  edits to strings required by the feature; leave unrelated wording to translators.
- Preserve Weblate commits with a **regular merge**, including when merging a
  translation PR. Do not squash or cherry-pick a conflict-recovery merge: Weblate
  must recognize its commits as ancestors of the upstream branch.
- Resolve JSON conflicts by key. If a feature deliberately removed a key from
  `en.json`, keep that key removed from translations too. Preserve all other
  translator changes and run `pnpm test:i18n` in `web/`.
- After pushing, check that Weblate has synchronized and is unlocked. A new
  GitHub commit alone does not confirm recovery. Do not reset Weblate or discard
  pending translations to clear an alert.

The component uses rebase when pulling upstream. A translation of a key removed
by a concurrent code change can therefore block synchronization, even when all
other translations merge cleanly. See Weblate's
[conflict recovery](https://docs.weblate.org/en/latest/faq.html#how-to-fix-merge-conflicts-in-translations)
and [conflict prevention](https://docs.weblate.org/en/latest/admin/continuous.html#avoiding-merge-conflicts)
guidance. Synchronizing first reduces conflicts; it cannot eliminate edits made
concurrently after that check.

## Translation credits

Thank you to the people behind the words, including contributors whose work
came through upstream NanoKVM:

| Contributor | Contribution | Attribution |
| --- | --- | --- |
| [Pavel Borecki (@p-bo)](https://github.com/p-bo) | Czech translation | [PR #207](https://github.com/dormancygrace/NanoKVM-OS/pull/207), human co-author in the Weblate commit |
| [Alexander-Ger-Reich](https://github.com/Alexander-Ger-Reich) | German and English UI maintenance | Upstream contribution acknowledged in the application Credits |
| [polyzium](https://github.com/polyzium) | Russian localization | Upstream contribution acknowledged in the application Credits |

**Hosted Weblate** is credited separately as our translation platform and
hosting provider, not as the person who wrote a translation.

Maintainers: read both authors and co-authors when reviewing translation PRs.
A Weblate bot may submit the work on someone's behalf. Keep this list, the
README, and the application's Credits in sync; thank new contributors in the
release that includes their work. Use public names or handles and verified
profile links, never email addresses. Suggestions and review contributions
deserve credit too when their authors can be identified.

### Hebrew starter

Hebrew (עברית, he) has a partial translation of common interface labels and
PicoClaw control messages in `locales/he.json`. Weblate uses the English source
strings; do not copy English values into the Hebrew file as if they were
translated. Untranslated strings fall back to English. This translation does
not introduce a full right-to-left layout.
