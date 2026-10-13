// The interface is translated on Hosted Weblate (see TRANSLATING.md). Locale
// files keep their historical names (cz, se, zh, zh_tw, pt_br); Weblate
// knows them through language aliases and links use its language codes.
const project = 'https://hosted.weblate.org/projects/nanokvm-os/web/';
const engage = 'https://hosted.weblate.org/engage/nanokvm-os/';

const weblateCodes: Record<string, string> = {
  'pt-BR': 'pt_BR',
  zh: 'zh_Hans',
  'zh-TW': 'zh_Hant'
};

// Where a reader of `language` (an i18next language tag) can fix or add
// translations. English is the source text, so it opens the project page.
export function translateUrl(language: string): string {
  if (language === 'en' || language.startsWith('en-')) return engage;
  return `${project}${weblateCodes[language] ?? language}/`;
}
