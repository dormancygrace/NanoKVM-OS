import type { BackendModule, i18n, ResourceLanguage } from 'i18next';

// Keep old saved locale names readable while giving i18next real language tags
// for regional detection, plural rules and Intl consumers.
const localeTags: Record<string, string> = {
  cz: 'cs',
  se: 'sv',
  pt_br: 'pt-BR',
  'pt-br': 'pt-BR',
  zh_tw: 'zh-TW',
  'zh-tw': 'zh-TW'
};

export function localeTag(name: string): string {
  return localeTags[name.toLowerCase()] ?? name;
}

export function selectLanguage(
  saved: string | null | undefined,
  browserLanguage: string,
  supported: readonly string[]
): string {
  const tags = supported.map(localeTag);
  const match = (name: string) =>
    tags.find((tag) => tag.toLowerCase() === localeTag(name).toLowerCase());

  return (
    (saved && match(saved)) ||
    match(browserLanguage) ||
    match(browserLanguage.split('-')[0]) ||
    'en'
  );
}

export function createLocaleBackend(
  loaders: Record<string, () => Promise<ResourceLanguage>>
): BackendModule {
  return {
    type: 'backend',
    init() {},
    read(language, namespace, callback) {
      const load = loaders[language];
      if (!load) {
        // Regional fallbacks without a file use the bundled English resources.
        callback(null, {});
        return;
      }
      load()
        .then((resources) => callback(null, resources[namespace] ?? {}))
        .catch((error: unknown) =>
          callback(error instanceof Error ? error : new Error(String(error)), false)
        );
    }
  };
}

// Keep the current language visible while its replacement loads. In particular,
// a failed chunk must not change or persist the user's current language.
export async function changeLoadedLanguage(instance: i18n, language: string): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    void instance.loadLanguages(language, (error) => {
      if (error) reject(error);
      else resolve();
    });
  });
  if (!instance.hasResourceBundle(language, 'translation')) {
    throw new Error('Locale could not be loaded');
  }
  await instance.changeLanguage(language);
}
