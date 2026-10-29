import { useCallback, useEffect, useState } from 'react';
import { Alert, Button } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { Panel, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

// Every network the device has joined. It joins those that connect
// automatically wherever it is; the setting is chosen when joining.
export const SavedWifi = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.network.wifiSaved.${key}`);
  const [networks, setNetworks] = useState<api.SavedWifiNetwork[]>();
  const [status, setStatus] = useState<api.WifiStatus>();
  const [failed, setFailed] = useState(false);
  const [busy, setBusy] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [saved, wifi] = await Promise.all([api.getSavedWifi(), api.getWiFi()]);
      if (saved.code !== 0) throw new Error();
      setNetworks(saved.data as api.SavedWifiNetwork[]);
      if (wifi.code === 0) setStatus(wifi.data as api.WifiStatus);
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    void refresh();
    return pollWhileVisible(() => void refresh(), 5000);
  }, [refresh]);

  const locked = busy || !!status?.busy;
  const canJoin = !!status?.supported && !status.apMode && status.enabled;

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

  async function join(network: api.SavedWifiNetwork) {
    if (locked) return;
    if (status?.thisConnection && status.connected) {
      const confirmed = await confirmAction({
        title: t('settings.network.wifi.joinConfirm', { ssid: network.ssid }),
        content: (
          <Alert type="warning" showIcon message={t('settings.network.wifi.joinOverWifi')} />
        ),
        okText: t('settings.network.wifi.confirmBtn'),
        danger: true
      });
      if (!confirmed) return;
    }
    setBusy(true);
    try {
      const rsp = await api.connectSavedWifi(network.ssid);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.operationFailed');
        return;
      }
      await refresh();
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setBusy(false);
    }
  }

  async function forget(network: api.SavedWifiNetwork) {
    if (locked) return;
    const leaving = network.current && !!status?.connected && status.ssid === network.ssid;
    if (
      !(await confirmAction({
        title: t('settings.network.wifiSaved.forgetConfirm', { ssid: network.ssid }),
        content: (
          <div className="space-y-2">
            <p>{tr(leaving ? 'forgetCurrentHint' : 'forgetHint')}</p>
            {leaving && status?.thisConnection && (
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
      setNetworks((old) => old?.filter((item) => item.ssid !== network.ssid));
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      {failed && <Alert type="error" showIcon message={t('settings.network.wifi.statusFailed')} />}
      <SettingsSection title={tr('title')} description={tr('description')}>
        {networks === undefined ? (
          !failed && (
            <div className="text-fg-muted text-xs">{t('settings.network.wifi.loading')}</div>
          )
        ) : networks.length === 0 ? (
          <div className="text-fg-muted text-xs">{tr('empty')}</div>
        ) : (
          <Panel flush className="divide-line divide-y">
            {networks.map((network) => {
              const connected = !!status?.connected && status.ssid === network.ssid;
              return (
                <div
                  key={network.ssid}
                  className="flex items-center justify-between gap-3 px-4 py-3"
                >
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
                        disabled={locked || !canJoin}
                        onClick={() => void join(network)}
                      >
                        {t('settings.network.wifi.joinBtn')}
                      </Button>
                    )}
                    <Button
                      size="small"
                      danger
                      disabled={locked}
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
    </div>
  );
};
