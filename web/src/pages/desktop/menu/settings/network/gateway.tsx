import { useCallback, useEffect, useState } from 'react';
import { Alert, Segmented } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingsSection } from '@/components/ui/settings.tsx';

export const Gateway = () => {
  const { t } = useTranslation();
  const ethernet = t('settings.network.ethernet.name');
  const wifi = t('settings.network.wifi.title');
  const labelFor = (route: api.GatewayRoute) =>
    `${route.interface.startsWith('eth') ? ethernet : wifi} · ${route.gateway}`;
  const [status, setStatus] = useState<api.GatewayStatus>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const rsp = await api.getGatewayPreference();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setStatus(rsp.data as api.GatewayStatus);
      setError('');
    } catch {
      setError(t('settings.network.gateway.loadFailed'));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  async function change(preferred: api.GatewayPreference) {
    if (!status || saving || preferred === status.preferred) return;
    setSaving(true);
    try {
      const rsp = await api.setGatewayPreference(preferred);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.gateway.saveFailed');
        return;
      }
      await load();
    } catch (err) {
      showRequestError(err, 'settings.network.gateway.saveFailed');
    } finally {
      setSaving(false);
    }
  }

  const routes = status?.routes || [];
  const available = new Set(
    routes.map((route) => (route.interface.startsWith('eth') ? 'ethernet' : 'wifi'))
  );

  return (
    <SettingsSection
      title={t('settings.network.gateway.title')}
      description={t('settings.network.gateway.description')}
    >
      {error && <Alert type="error" showIcon message={error} />}
      <Segmented
        block
        value={status?.preferred || 'auto'}
        disabled={!status || saving}
        onChange={(value) => void change(value as api.GatewayPreference)}
        options={[
          { label: t('settings.network.gateway.auto'), value: 'auto' },
          { label: ethernet, value: 'ethernet', disabled: !available.has('ethernet') },
          { label: wifi, value: 'wifi', disabled: !available.has('wifi') }
        ]}
      />
      {status && routes.length === 0 ? (
        <div className="text-fg-muted text-xs">{t('settings.network.gateway.none')}</div>
      ) : status ? (
        <div className="text-fg-muted space-y-1 text-xs">
          {routes.map((route) => (
            <div key={`${route.interface}-${route.gateway}`}>
              {labelFor(route)} · {t('settings.network.gateway.metric', { value: route.metric })}
            </div>
          ))}
        </div>
      ) : null}
    </SettingsSection>
  );
};
