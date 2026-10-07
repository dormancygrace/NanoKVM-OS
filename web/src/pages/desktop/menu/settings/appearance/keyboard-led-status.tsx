import { Switch } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as storage from '@/lib/localstorage.ts';
import { keyboardLedStatusVisibleAtom } from '@/jotai/settings.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const KeyboardLedStatusSetting = () => {
  const { t } = useTranslation();
  const [visible, setVisible] = useAtom(keyboardLedStatusVisibleAtom);

  function update(nextVisible: boolean) {
    setVisible(nextVisible);
    storage.setKeyboardLedStatusVisible(nextVisible);
  }

  return (
    <SettingRow
      label={t('settings.appearance.menuBar.keyboardLedStatus')}
      description={t('settings.appearance.menuBar.keyboardLedStatusDesc')}
      htmlFor="appearance-keyboard-led-status"
    >
      <Switch
        id="appearance-keyboard-led-status"
        aria-describedby="appearance-keyboard-led-status-description"
        checked={visible}
        onChange={update}
      />
    </SettingRow>
  );
};
