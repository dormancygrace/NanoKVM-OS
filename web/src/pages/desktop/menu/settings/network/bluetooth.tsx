import { useEffect, useState } from 'react';
import { Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

// Network > Bluetooth: in the menu only where the Wi-Fi chip has Bluetooth
// and the image ships its driver.
export const Bluetooth = () => {
  const { t } = useTranslation();

  const [state, setState] = useState<api.BluetoothState | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  useEffect(() => {
    let active = true;
    api
      .getBluetoothState()
      .then((rsp) => {
        if (active && rsp.code === 0) setState(rsp.data);
      })
      .catch(() => {})
      .finally(() => {
        if (active) setIsLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  async function update() {
    if (isLoading || !state) return;
    setIsLoading(true);
    try {
      const rsp = state.enabled ? await api.disableBluetooth() : await api.enableBluetooth();
      if (rsp.code !== 0) {
        showRequestError(rsp);
        return;
      }
      const next = await api.getBluetoothState();
      if (next.code === 0) setState(next.data);
    } catch (err) {
      // Keep the last confirmed state if the request fails.
      showRequestError(err);
    } finally {
      setIsLoading(false);
    }
  }

  if (!state) return null;
  if (!state.available) {
    return (
      <p className="text-sm text-neutral-400">{t('settings.network.bluetooth.unavailable')}</p>
    );
  }

  return (
    <div className="space-y-6">
      <SettingsSection>
        <SettingRow
          label="Bluetooth"
          description={
            state.loaded ? t('settings.network.bluetooth.on') : t('settings.network.bluetooth.off')
          }
          help={t('settings.network.bluetooth.tip')}
          htmlFor="network-bluetooth"
        >
          <Switch
            id="network-bluetooth"
            aria-label="Bluetooth"
            aria-describedby="network-bluetooth-description"
            checked={state.enabled}
            loading={isLoading}
            onChange={update}
          />
        </SettingRow>
      </SettingsSection>
      {/* Only what the running controller reported. */}
      {state.loaded && state.version && (
        <SettingsSection>
          <SettingRow label={t('settings.network.bluetooth.version')}>
            <span className="text-right">{state.version}</span>
          </SettingRow>
        </SettingsSection>
      )}
    </div>
  );
};
