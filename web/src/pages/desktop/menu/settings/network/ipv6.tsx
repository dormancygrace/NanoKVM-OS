import { useCallback, useEffect, useState } from 'react';
import { Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { getIPv6, setIPv6 } from '@/api/network.ts';
import type { IPv6Status } from '@/api/network.ts';

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
    const timer = window.setInterval(() => void refresh(), 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  async function change(enabled: boolean) {
    if (saving) return;
    setSaving(true);
    setError('');
    try {
      const rsp = await setIPv6(enabled);
      if (rsp.code !== 0) throw new Error();
      await refresh();
    } catch {
      setError(t('settings.network.ipv6.saveFailed'));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-6">
        <div className="flex flex-col gap-1">
          <span>IPv6</span>
          <span className="text-xs text-neutral-500">{t('settings.network.ipv6.description')}</span>
        </div>
        <Switch
          aria-label={t('settings.network.ipv6.enable')}
          checked={status?.enabled ?? false}
          loading={saving}
          disabled={!status?.supported || saving}
          onChange={(enabled) => void change(enabled)}
        />
      </div>
      {status && !status.supported && (
        <div className="text-xs text-neutral-500">{t('settings.network.ipv6.unsupported')}</div>
      )}
      {status?.enabled && (
        <div className="space-y-3 rounded-xl bg-neutral-800/50 p-4">
          {status.addresses.length === 0 ? (
            <div className="text-sm text-neutral-400">{t('settings.network.ipv6.waiting')}</div>
          ) : (
            status.addresses.map((address) => (
              <div key={`${address.interface}/${address.address}`} className="space-y-1">
                <div className="flex items-center gap-2 text-xs text-neutral-400">
                  <span>{address.interface}</span>
                  <span>· {t(`settings.network.ipv6.${address.scope}`)}</span>
                  {address.mtu > 0 && <span>· MTU {address.mtu}</span>}
                </div>
                <div className="select-text break-all font-mono text-sm text-neutral-200">
                  {address.address}
                </div>
              </div>
            ))
          )}
          <div className="text-xs text-neutral-500">
            {t('settings.network.ipv6.disconnectHint')}
          </div>
        </div>
      )}
      {error && (
        <div role="alert" className="text-xs text-red-400">
          {error}
        </div>
      )}
    </div>
  );
};
