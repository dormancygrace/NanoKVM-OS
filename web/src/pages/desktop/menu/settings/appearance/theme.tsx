import { Segmented } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as storage from '@/lib/localstorage.ts';
import type { Theme as ThemeName } from '@/lib/theme-tokens.ts';
import { themeAtom } from '@/jotai/settings.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const Theme = () => {
  const { t } = useTranslation();
  const [theme, setTheme] = useAtom(themeAtom);

  const options: { value: ThemeName; label: string }[] = [
    { value: 'dark', label: t('settings.appearance.themeDark') },
    { value: 'light', label: t('settings.appearance.themeLight') }
  ];

  function handleChange(value: ThemeName) {
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
