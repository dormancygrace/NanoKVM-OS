import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Checkbox, Input, message, Segmented, Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import { startNetworkTrial } from '@/lib/network-trial.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

// The channels the server accepts (service/network/wifi_ap.go): those
// without radar detection.
const channels: Record<api.WifiBand, number[]> = {
  '2.4': [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13],
  '5': [36, 40, 44, 48]
};

const validPassword = (value: string) =>
  value.length >= 8 && value.length <= 63 && value.trim() === value && /^[\x20-\x7e]*$/.test(value);
const validSubnet = (value: string) =>
  value === '' ||
  (/^(10\.\d{1,3}\.\d{1,3}|172\.(1[6-9]|2\d|3[01])\.\d{1,3}|192\.168\.\d{1,3})$/.test(value) &&
    value.split('.').every((octet) => Number(octet) <= 255));
const ssidBytes = (value: string) => new TextEncoder().encode(value).length;
const dnsList = (value: string) => value.split(/[\s,]+/).filter(Boolean);
const validDns = (value: string) => {
  const servers = dnsList(value);
  return (
    servers.length <= 3 &&
    servers.every(
      (server) =>
        /^\d{1,3}(\.\d{1,3}){3}$/.test(server) &&
        server.split('.').every((octet) => Number(octet) <= 255) &&
        server !== '0.0.0.0'
    )
  );
};
const duration = (seconds: number) => {
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = seconds % 60;
  return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
};

// The administrator's access point: Wi-Fi serves a network of its own
// instead of joining one. Saved on trial, as a change that can cut this
// page off.
export const WifiAccessPoint = ({ onChanged }: { onChanged?: () => void }) => {
  const { t } = useTranslation();
  const tr = (key: string, options?: Record<string, unknown>) =>
    t(`settings.network.wifiAp.${key}`, options);
  const [status, setStatus] = useState<api.WifiAPStatus>();
  const [form, setForm] = useState<api.WifiAP>();
  const [password, setPassword] = useState('');
  // As typed; undefined until edited.
  const [dnsText, setDnsText] = useState<string>();
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    try {
      const rsp = await api.getWifiAP();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setStatus(rsp.data);
      setForm((current) => current ?? { ...rsp.data, password: undefined });
      setError('');
    } catch {
      setError(t('settings.network.wifiAp.loadFailed'));
    }
  }, [t]);

  useEffect(() => {
    void load();
  }, [load]);

  // Follow the access point coming up and the devices joining it.
  useEffect(() => {
    if (!status?.enabled) return;
    const timer = window.setInterval(() => void load(), 5000);
    return () => window.clearInterval(timer);
  }, [status?.enabled, load]);

  if (!form) return error ? <Alert type="error" showIcon message={error} /> : null;

  const set = (patch: Partial<api.WifiAP>) => setForm({ ...form, ...patch });
  const ssidValid = ssidBytes(form.ssid) >= 1 && ssidBytes(form.ssid) <= 32;
  const passwordNeeded = !status?.passwordSet;
  const passwordValid = password === '' ? !passwordNeeded : validPassword(password);
  const subnetValid = validSubnet(form.subnet);
  const dns = dnsText ?? (form.dns ?? []).join(', ');
  const dnsValid = validDns(dns);
  const valid = ssidValid && passwordValid && subnetValid && dnsValid;

  async function save() {
    if (!form || !valid || saving) return;
    setSaving(true);
    try {
      const rsp = await api.setWifiAP({
        ...form,
        password: password || undefined,
        dns: dnsList(dns)
      });
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifiAp.saveFailed');
        return;
      }
      // A trial only when the page itself goes over Wi-Fi.
      if (rsp.data?.trial) startNetworkTrial(rsp.data.trial);
      else void message.success(tr('saved'));
      setPassword('');
      await load();
      onChanged?.();
    } catch (err) {
      showRequestError(err, 'settings.network.wifiAp.saveFailed');
    } finally {
      setSaving(false);
    }
  }

  let state = null;
  if (status?.enabled) {
    state = status.running ? (
      <StatusBadge tone="success">
        {tr('state', { address: status.address, count: status.stations.length })}
      </StatusBadge>
    ) : (
      <StatusBadge tone="neutral">{tr('starting')}</StatusBadge>
    );
  }

  return (
    <SettingsSection title={tr('title')} description={tr('description')}>
      {error && <Alert type="error" showIcon message={error} />}
      <SettingRow
        htmlFor="wifi-ap-enabled"
        label={tr('enable')}
        description={state ?? tr('enableDescription')}
      >
        <Switch
          id="wifi-ap-enabled"
          aria-describedby="wifi-ap-enabled-description"
          checked={form.enabled}
          disabled={saving}
          onChange={(enabled) => set({ enabled })}
        />
      </SettingRow>
      {status?.running && (
        <div className="space-y-2">
          <div className="text-sm">{tr('stations')}</div>
          {status.stations.length === 0 ? (
            <div className="text-fg-muted text-xs">{tr('noStations')}</div>
          ) : (
            <Panel flush className="divide-line divide-y">
              {status.stations.map((station) => (
                <div
                  key={station.mac}
                  className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 py-3"
                >
                  <div className="min-w-0">
                    <div className="text-sm break-all">
                      {station.hostname || station.ip || station.mac}
                    </div>
                    <div className="text-fg-muted text-xs">
                      {[station.hostname && station.ip, station.mac].filter(Boolean).join(' · ')}
                    </div>
                  </div>
                  <div className="text-fg-muted text-xs tabular-nums">
                    {[
                      station.signal !== undefined && `${station.signal} dBm`,
                      station.rate && `${station.rate} Mbit/s`,
                      duration(station.connected)
                    ]
                      .filter(Boolean)
                      .join(' · ')}
                  </div>
                </div>
              ))}
            </Panel>
          )}
        </div>
      )}
      <SettingRow
        stacked
        htmlFor="wifi-ap-ssid"
        label={tr('ssid')}
        description={ssidValid ? undefined : tr('invalidSsid')}
      >
        <Input
          id="wifi-ap-ssid"
          value={form.ssid}
          status={ssidValid ? undefined : 'error'}
          disabled={saving}
          onChange={(event) => set({ ssid: event.target.value })}
        />
      </SettingRow>
      <SettingRow
        stacked
        htmlFor="wifi-ap-password"
        label={tr('password')}
        description={passwordValid ? tr('passwordHint') : tr('invalidPassword')}
      >
        <Input.Password
          id="wifi-ap-password"
          aria-describedby="wifi-ap-password-description"
          autoComplete="new-password"
          placeholder={status?.passwordSet ? tr('passwordKeep') : undefined}
          value={password}
          status={passwordValid ? undefined : 'error'}
          disabled={saving}
          onChange={(event) => setPassword(event.target.value)}
        />
      </SettingRow>
      <SettingRow label={tr('band')}>
        <Segmented
          value={form.band}
          disabled={saving}
          onChange={(band) => set({ band: band as api.WifiBand, channel: 0 })}
          options={[
            { label: tr('band24'), value: '2.4' },
            { label: tr('band5'), value: '5' }
          ]}
        />
      </SettingRow>
      <SettingRow htmlFor="wifi-ap-channel" label={tr('channel')}>
        <Select
          id="wifi-ap-channel"
          className="min-w-36"
          value={form.channel}
          disabled={saving}
          onChange={(channel) => set({ channel })}
          options={[
            { label: tr('channelAuto'), value: 0 },
            ...channels[form.band].map((channel) => ({ label: String(channel), value: channel }))
          ]}
        />
      </SettingRow>
      <SettingRow label={tr('hidden')} description={tr('hiddenDescription')}>
        <Checkbox
          checked={form.hidden}
          disabled={saving}
          aria-label={tr('hidden')}
          onChange={(event) => set({ hidden: event.target.checked })}
        />
      </SettingRow>
      <SettingRow label={tr('internet')} description={tr('internetDescription')}>
        <Checkbox
          checked={form.internet}
          disabled={saving}
          aria-label={tr('internet')}
          onChange={(event) => set({ internet: event.target.checked })}
        />
      </SettingRow>
      {form.internet && (
        <SettingRow
          stacked
          htmlFor="wifi-ap-dns"
          label={tr('dns')}
          description={dnsValid ? tr('dnsDescription') : tr('invalidDns')}
        >
          <Input
            id="wifi-ap-dns"
            aria-describedby="wifi-ap-dns-description"
            placeholder={tr('dnsPlaceholder')}
            value={dns}
            status={dnsValid ? undefined : 'error'}
            disabled={saving}
            onChange={(event) => setDnsText(event.target.value)}
          />
        </SettingRow>
      )}
      <SettingRow
        stacked
        htmlFor="wifi-ap-subnet"
        label={tr('subnet')}
        description={subnetValid ? tr('subnetDescription') : tr('invalidSubnet')}
      >
        <Input
          id="wifi-ap-subnet"
          aria-describedby="wifi-ap-subnet-description"
          placeholder={tr('subnetPlaceholder')}
          value={form.subnet}
          status={subnetValid ? undefined : 'error'}
          disabled={saving}
          onChange={(event) => set({ subnet: event.target.value.trim() })}
        />
      </SettingRow>
      <Alert type="warning" showIcon message={tr('warning')} />
      <div>
        <Button type="primary" loading={saving} disabled={!valid} onClick={() => void save()}>
          {tr('save')}
        </Button>
      </div>
    </SettingsSection>
  );
};
