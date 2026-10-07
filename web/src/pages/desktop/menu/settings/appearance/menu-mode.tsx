import { Segmented } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as storage from '@/lib/localstorage.ts';
import { menuDisplayModeAtom } from '@/jotai/settings.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const MenuMode = () => {
  const { t } = useTranslation();

  const [menuDisplayMode, setMenuDisplayMode] = useAtom(menuDisplayModeAtom);

  const options = [
    // { value: 'off', label: t('settings.appearance.menuBar.modeOff') },
    { value: 'auto', label: t('settings.appearance.menuBar.modeAuto') },
    { value: 'always', label: t('settings.appearance.menuBar.modeAlways') }
  ];

  function handleChange(mode: string) {
    if (mode === menuDisplayMode) return;

    setMenuDisplayMode(mode);
    storage.setMenuDisplayMode(mode);
  }

  return (
    <SettingRow
      label={t('settings.appearance.menuBar.mode')}
      description={t('settings.appearance.menuBar.modeDesc')}
    >
      <Segmented value={menuDisplayMode} options={options} onChange={handleChange} />
    </SettingRow>
  );
};
