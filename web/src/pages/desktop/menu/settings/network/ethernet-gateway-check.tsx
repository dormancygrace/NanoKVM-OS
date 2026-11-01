import { useCallback, useEffect, useState } from 'react';
import { Alert, Input, Segmented, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

// The server's rule for the watched host (service/network/gateway_check.go).
const hostPattern = /^[A-Za-z0-9][A-Za-z0-9.:-]{0,252}$/;

// The Ethernet gateway check, saved on change: while the Ethernet gateway
// (or the host behind it) does not answer and Wi-Fi is connected, traffic
// leaves through Wi-Fi.
export const EthernetGatewayCheck = () => {
  const { t } = useTranslation();
  const key = (name: string) => `settings.network.ethernet.gatewayCheck.${name}`;
  const [status, setStatus] = useState<api.GatewayCheckStatus>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [host, setHost] = useState('');

  const load = useCallback(async () => {
    try {
      const rsp = await api.getGatewayCheck();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setStatus(rsp.data);
      setError('');
    } catch {
      setError(t(key('loadFailed')));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (status) setHost(status.host);
  }, [status?.host]); // eslint-disable-line react-hooks/exhaustive-deps

  // The check runs every 5 s on the device: follow it while it is on.
  useEffect(() => {
    if (!status?.enabled) return;
    const timer = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(timer);
  }, [status?.enabled, load]);

  async function save(next: api.GatewayCheck) {
    if (saving) return;
    setSaving(true);
    try {
      const rsp = await api.setGatewayCheck(next);
      if (rsp.code !== 0) {
        showRequestError(rsp, key('saveFailed'));
        return;
      }
      await load();
    } catch (err) {
      showRequestError(err, key('saveFailed'));
    } finally {
      setSaving(false);
    }
  }

  const check: api.GatewayCheck | undefined = status && {
    enabled: status.enabled,
    method: status.method,
    host: status.host
  };
  const hostValue = host.trim();
  const hostValid = hostValue === '' || hostPattern.test(hostValue);
  function commitHost() {
    if (check && hostValid && hostValue !== check.host) void save({ ...check, host: hostValue });
  }

  let state = null;
  if (status?.enabled && status.gateway) {
    if (status.demoted) state = <StatusBadge tone="warning">{t(key('bypassed'))}</StatusBadge>;
    else if (status.reachable === true)
      state = <StatusBadge tone="success">{t(key('answers'))}</StatusBadge>;
    else if (status.reachable === false)
      state = <StatusBadge tone="danger">{t(key('silent'))}</StatusBadge>;
  }

  return (
    <SettingsSection
      title={t(key('title'))}
      description={t(key(status?.wifi === false ? 'descriptionNoWifi' : 'description'))}
    >
      {error && <Alert type="error" showIcon message={error} />}
      <SettingRow
        htmlFor="ethernet-gateway-check"
        label={t(key('enable'))}
        description={
          status?.gateway ? (
            <span className="inline-flex flex-wrap items-center gap-x-2">
              <span>{t(key('gateway'), { address: status.gateway })}</span>
              {state}
            </span>
          ) : status ? (
            t(key('noGateway'))
          ) : undefined
        }
      >
        <Switch
          id="ethernet-gateway-check"
          aria-describedby="ethernet-gateway-check-description"
          checked={!!status?.enabled}
          disabled={!check || saving}
          onChange={(enabled) => check && void save({ ...check, enabled })}
        />
      </SettingRow>
      {check?.enabled && (
        <>
          <SettingRow label={t(key('method'))}>
            <Segmented
              value={check.method}
              disabled={saving}
              onChange={(method) =>
                void save({ ...check, method: method as api.GatewayCheckMethod })
              }
              options={[
                { label: t(key('methodArp')), value: 'arp' },
                { label: t(key('methodIcmp')), value: 'icmp' }
              ]}
            />
          </SettingRow>
          <SettingRow
            stacked
            htmlFor="ethernet-gateway-check-host"
            label={t(key('host'))}
            description={hostValid ? t(key('hostDescription')) : t(key('hostInvalid'))}
          >
            <Input
              id="ethernet-gateway-check-host"
              aria-describedby="ethernet-gateway-check-host-description"
              aria-invalid={!hostValid}
              status={hostValid ? undefined : 'error'}
              placeholder="1.1.1.1"
              value={host}
              maxLength={253}
              disabled={saving}
              onChange={(event) => setHost(event.target.value)}
              onBlur={commitHost}
              onPressEnter={commitHost}
            />
          </SettingRow>
        </>
      )}
    </SettingsSection>
  );
};
