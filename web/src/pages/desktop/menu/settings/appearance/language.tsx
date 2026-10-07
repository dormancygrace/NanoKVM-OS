import { useState } from 'react';
import { message, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import languages from '@/i18n/languages.ts';
import { changeLoadedLanguage, localeTag } from '@/i18n/locale-loader.ts';
import { setLanguage } from '@/lib/localstorage.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const Language = () => {
  const { t, i18n } = useTranslation();
  const [loading, setLoading] = useState(false);

  const options = languages.map((language) => ({
    value: localeTag(language.key),
    label: language.partial ? (
      <span title={t('settings.appearance.languagePartialHint')}>
        {language.name}{' '}
        <span className="text-fg-muted text-xs">({t('settings.appearance.languagePartial')})</span>
      </span>
    ) : (
      language.name
    )
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
    <SettingRow
      label={t('settings.appearance.language')}
      description={t('settings.appearance.languageDesc')}
      htmlFor="appearance-language"
    >
      <Select
        id="appearance-language"
        aria-describedby="appearance-language-description"
        value={i18n.language}
        loading={loading}
        disabled={loading}
        style={{ width: 180 }}
        options={options}
        onSelect={changeLanguage}
      />
    </SettingRow>
  );
};
