// `partial` marks locales that translate well under 90% of the English keys;
// the rest of their interface falls back to English. tests/i18n-keys.test.mjs
// requires every locale without the flag to be complete.
const languages = [
  { key: 'ca', name: 'Català', partial: true },
  { key: 'nl', name: 'Nederlands', partial: true },
  { key: 'da', name: 'Dansk', partial: true },
  { key: 'de', name: 'Deutsch', partial: true },
  { key: 'en', name: 'English' },
  { key: 'es', name: 'Español', partial: true },
  { key: 'fr', name: 'Français', partial: true },
  { key: 'id', name: 'Bahasa Indonesia', partial: true },
  { key: 'it', name: 'Italiano', partial: true },
  { key: 'pl', name: 'Polski', partial: true },
  { key: 'pt_br', name: 'Português (Brasil)', partial: true },
  { key: 'ru', name: 'Русский' },
  { key: 'tr', name: 'Türkçe', partial: true },
  { key: 'ko', name: '한국어', partial: true },
  { key: 'zh', name: '简体中文', partial: true },
  { key: 'zh_tw', name: '繁體中文', partial: true },
  { key: 'hu', name: 'Magyar', partial: true },
  { key: 'vi', name: 'Tiếng Việt', partial: true },
  { key: 'ja', name: '日本語', partial: true },
  { key: 'cz', name: 'Česky', partial: true },
  { key: 'uk', name: 'Українська', partial: true },
  { key: 'nb', name: 'Norsk, bokmål', partial: true },
  { key: 'th', name: 'ภาษาไทย', partial: true },
  { key: 'se', name: "Svenska", partial: true }
];

languages.sort((a, b) => a.name.localeCompare(b.name, 'en', { sensitivity: 'base' }));

export default languages;
