import { Segmented } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as storage from '@/lib/localstorage.ts';
import type { ThemeSetting } from '@/lib/theme.ts';
import { themeSettingAtom } from '@/jotai/settings.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const Theme = () => {
  const { t } = useTranslation();
  const [theme, setTheme] = useAtom(themeSettingAtom);

  const options: { value: ThemeSetting; label: string }[] = [
    { value: 'system', label: t('settings.appearance.themeSystem') },
    { value: 'dark', label: t('settings.appearance.themeDark') },
    { value: 'light', label: t('settings.appearance.themeLight') }
  ];

  function handleChange(value: ThemeSetting) {
    setTheme(value);
    storage.setTheme(value);
  }

  return (
    <SettingRow
      label={t('settings.appearance.theme')}
      description={t('settings.appearance.themeDesc')}
    >
      <Segmented value={theme} options={options} onChange={handleChange} />
    </SettingRow>
  );
};
