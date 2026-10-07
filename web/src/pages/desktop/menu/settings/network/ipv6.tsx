import { useCallback, useEffect, useState } from 'react';
import { Alert, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { getIPv6, setIPv6 } from '@/api/network.ts';
import type { IPv6Status } from '@/api/network.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

export const IPv6 = () => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<IPv6Status | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const refresh = useCallback(async () => {
    try {
      const rsp = await getIPv6();
      if (rsp.code !== 0) throw new Error();
      setStatus(rsp.data as IPv6Status);
      setError('');
    } catch {
      setError(t('settings.network.ipv6.loadFailed'));
    }
  }, [t]);

  useEffect(() => {
    void refresh();
    // SLAAC addresses arrive after enabling; keep status current while open.
    const stopPolling = pollWhileVisible(() => void refresh(), 5000);
    return () => stopPolling();
  }, [refresh]);

  async function change(enabled: boolean) {
    if (saving) return;
    setSaving(true);
    try {
      const rsp = await setIPv6(enabled);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.ipv6.saveFailed');
        return;
      }
      await refresh();
    } catch (err) {
      showRequestError(err, 'settings.network.ipv6.saveFailed');
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="space-y-3">
      {error && <Alert type="error" showIcon message={error} />}
      <SettingRow
        label="IPv6"
        description={t('settings.network.ipv6.description')}
        htmlFor="network-ipv6"
      >
        <Switch
          id="network-ipv6"
          aria-label={t('settings.network.ipv6.enable')}
          aria-describedby="network-ipv6-description"
          checked={status?.enabled ?? false}
          loading={saving}
          disabled={!status?.supported || saving}
          onChange={(enabled) => void change(enabled)}
        />
      </SettingRow>
      {status && !status.supported && (
        <div className="text-fg-muted text-xs">{t('settings.network.ipv6.unsupported')}</div>
      )}
      {status?.enabled && (
        <Panel className="space-y-3">
          {status.addresses.length === 0 ? (
            <div className="text-fg-muted text-sm">{t('settings.network.ipv6.waiting')}</div>
          ) : (
            status.addresses.map((address) => (
              <div key={`${address.interface}/${address.address}`} className="space-y-1">
                <div className="text-fg-muted flex items-center gap-2 text-xs">
                  <span>{address.interface}</span>
                  <span>· {t(`settings.network.ipv6.${address.scope}`)}</span>
                  {address.mtu > 0 && <span>· MTU {address.mtu}</span>}
                </div>
                <div className="text-fg font-mono text-sm break-all select-text">
                  {address.address}
                </div>
              </div>
            ))
          )}
          <div className="text-fg-muted text-xs">{t('settings.network.ipv6.disconnectHint')}</div>
        </Panel>
      )}
    </div>
  );
};
