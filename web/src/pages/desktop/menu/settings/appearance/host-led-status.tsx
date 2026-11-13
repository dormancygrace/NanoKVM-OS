import { Switch } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as storage from '@/lib/localstorage.ts';
import { hostLedStatusVisibleAtom } from '@/jotai/settings.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const HostLedStatusSetting = () => {
  const { t } = useTranslation();
  const [visible, setVisible] = useAtom(hostLedStatusVisibleAtom);

  function update(nextVisible: boolean) {
    setVisible(nextVisible);
    storage.setHostLedStatusVisible(nextVisible);
  }

  return (
    <SettingRow
      label={t('settings.appearance.menuBar.hostLedStatus')}
      description={t('settings.appearance.menuBar.hostLedStatusDesc')}
      htmlFor="appearance-host-led-status"
    >
      <Switch
        id="appearance-host-led-status"
        aria-describedby="appearance-host-led-status-description"
        checked={visible}
        onChange={update}
      />
    </SettingRow>
  );
};
