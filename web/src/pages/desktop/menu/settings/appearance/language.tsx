import { useState } from 'react';
import { message, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import languages from '@/i18n/languages.ts';
import { changeLoadedLanguage, localeTag } from '@/i18n/locale-loader.ts';
import { setLanguage } from '@/lib/localstorage.ts';

export const Language = () => {
  const { t, i18n } = useTranslation();
  const [loading, setLoading] = useState(false);

  const options = languages.map((language) => ({
    value: localeTag(language.key),
    label: language.name
  }));

  async function changeLanguage(value: string) {
    if (i18n.language === value || loading) return;

    setLoading(true);
    try {
      await changeLoadedLanguage(i18n, value);
      setLanguage(value);
    } catch {
      message.error(t('error.title'));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="mt-5 flex items-center justify-between space-x-5">
      <div className="flex flex-col space-y-1">
        <span>{t('settings.appearance.language')}</span>
        <span className="text-xs text-neutral-500">{t('settings.appearance.languageDesc')}</span>
      </div>

      <div>
        <Select
          value={i18n.language}
          loading={loading}
          disabled={loading}
          style={{ width: 180 }}
          options={options}
          onSelect={changeLanguage}
        />
      </div>
    </div>
  );
};
