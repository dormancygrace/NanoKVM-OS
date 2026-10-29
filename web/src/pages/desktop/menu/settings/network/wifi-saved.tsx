import { useState } from 'react';
import { Alert, Button } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

type Props = {
  networks: api.SavedWifiNetwork[];
  state: api.WifiStatus;
  locked: boolean;
  canJoin: boolean;
  onJoin: (ssid: string) => void;
  onChanged: () => void;
};

// Every network the device has joined, on the Wi-Fi page. It joins those that
// connect automatically wherever it is; the setting is chosen when joining.
export const SavedWifi = ({ networks, state, locked, canJoin, onJoin, onChanged }: Props) => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.network.wifiSaved.${key}`);
  const [busy, setBusy] = useState(false);

  function securityLabel(security: api.SavedWifiNetwork['security']) {
    const labels: Record<string, string> = {
      wpa: 'WPA',
      'wpa-wpa2': 'WPA/WPA2',
      wpa2: 'WPA2',
      wpa3: 'WPA3',
      'wpa2-wpa3': 'WPA2/WPA3',
      personal: 'WPA2/WPA3'
    };
    return security === 'open' ? t('settings.network.wifi.open') : labels[security];
  }

  async function forget(network: api.SavedWifiNetwork) {
    if (locked || busy) return;
    const leaving = network.current && state.connected && state.ssid === network.ssid;
    if (
      !(await confirmAction({
        title: t('settings.network.wifiSaved.forgetConfirm', { ssid: network.ssid }),
        content: (
          <div className="space-y-2">
            <p>{tr(leaving ? 'forgetCurrentHint' : 'forgetHint')}</p>
            {leaving && state.thisConnection && (
              <Alert
                type="warning"
                showIcon
                message={t('settings.network.wifi.disconnectOverWifi')}
              />
            )}
          </div>
        ),
        okText: tr('forget'),
        danger: true
      }))
    )
      return;
    setBusy(true);
    try {
      const rsp = await api.forgetSavedWifi(network.ssid);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.operationFailed');
        return;
      }
      onChanged();
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingsSection description={tr('description')}>
      {networks.length === 0 ? (
        <div className="text-fg-muted text-xs">{tr('empty')}</div>
      ) : (
        <Panel flush className="divide-line divide-y">
          {networks.map((network) => {
            const connected = state.connected && state.ssid === network.ssid;
            return (
              <div key={network.ssid} className="flex items-center justify-between gap-3 px-4 py-3">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="break-all">{network.ssid}</span>
                    {connected && <StatusBadge tone="success">{tr('connected')}</StatusBadge>}
                  </div>
                  <div className="text-fg-muted text-xs">
                    {securityLabel(network.security)}
                    {network.hidden && ` · ${t('settings.network.wifi.hidden')}`}
                    {' · '}
                    {tr(network.autoConnect ? 'automatic' : 'manual')}
                  </div>
                </div>
                <div className="flex shrink-0 gap-2">
                  {!connected && (
                    <Button
                      size="small"
                      disabled={locked || busy || !canJoin}
                      onClick={() => onJoin(network.ssid)}
                    >
                      {t('settings.network.wifi.joinBtn')}
                    </Button>
                  )}
                  <Button
                    size="small"
                    danger
                    disabled={locked || busy}
                    onClick={() => void forget(network)}
                  >
                    {tr('forget')}
                  </Button>
                </div>
              </div>
            );
          })}
        </Panel>
      )}
    </SettingsSection>
  );
};
