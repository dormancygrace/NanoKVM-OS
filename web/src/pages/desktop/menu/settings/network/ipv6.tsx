import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Input, Segmented, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { getIPv6, setIPv6 } from '@/api/network.ts';
import type { IPv6Interface, IPv6Mode, IPv6Status } from '@/api/network.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

// A rough check before the server's: an IPv6 address with a prefix length.
const looksLikePrefix = (value: string) => /^[0-9a-f:.]+\/\d{1,3}$/i.test(value.trim());
const looksLikeAddress = (value: string) => /^[0-9a-f:.]+$/i.test(value.trim());

/*
 * IPv6 of one interface (Ethernet or Wi-Fi), configured as IPv4 is:
 * automatically (SLAAC from router advertisements and DHCPv6) or with a
 * static address. Other interfaces, such as the USB network and VPNs, have
 * IPv6 when either of the two has it.
 */
export const IPv6 = ({ name }: { name: IPv6Interface['name'] }) => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.network.ipv6.${key}`);
  const [status, setStatus] = useState<IPv6Status | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  // The static address being edited, until it is saved.
  const [draft, setDraft] = useState<{ mode: IPv6Mode; address: string; gateway: string }>();

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

  async function apply(setting: Parameters<typeof setIPv6>[1]) {
    if (saving) return;
    setSaving(true);
    try {
      const rsp = await setIPv6(name, setting);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.ipv6.saveFailed');
        return;
      }
      setDraft(undefined);
      await refresh();
    } catch (err) {
      showRequestError(err, 'settings.network.ipv6.saveFailed');
    } finally {
      setSaving(false);
    }
  }

  const iface = status?.interfaces?.find((item) => item.name === name);
  const enabled = iface?.enabled ?? false;
  const saved = {
    mode: (iface?.mode || 'dhcpv6') as IPv6Mode,
    address: iface?.address ?? '',
    gateway: iface?.gateway ?? ''
  };
  const shown = draft ?? saved;
  const dirty =
    !!draft &&
    (draft.mode !== saved.mode ||
      draft.address.trim() !== saved.address ||
      draft.gateway.trim() !== saved.gateway);
  const invalid =
    shown.mode === 'static' &&
    (!looksLikePrefix(shown.address) ||
      (!!shown.gateway.trim() && !looksLikeAddress(shown.gateway)));
  const addresses = status?.addresses.filter((address) => address.interface === name) ?? [];
  const id = `ipv6-${name}`;

  function chooseMode(mode: IPv6Mode) {
    // Automatic needs nothing more; static waits for its address and Save.
    if (mode === 'dhcpv6') {
      void apply({ enabled: true, mode });
      return;
    }
    setDraft({ ...shown, mode });
  }

  return (
    <SettingsSection title="IPv6">
      {error && <Alert type="error" showIcon message={error} />}
      <SettingRow label="IPv6" description={tr('description')} htmlFor={id}>
        <Switch
          id={id}
          aria-label={tr('enable')}
          aria-describedby={`${id}-description`}
          checked={enabled}
          loading={saving}
          disabled={!status?.supported || !iface || saving}
          onChange={(next) => void apply({ enabled: next })}
        />
      </SettingRow>
      {status && !status.supported && (
        <div className="text-fg-muted text-xs">{tr('unsupported')}</div>
      )}
      {enabled && (
        <>
          <SettingRow
            label={tr('configuration')}
            description={
              shown.mode === 'static' ? tr('staticDescription') : tr('dhcpv6Description')
            }
          >
            <Segmented
              disabled={saving}
              value={shown.mode}
              onChange={(mode) => chooseMode(mode as IPv6Mode)}
              options={[
                { label: 'DHCPv6', value: 'dhcpv6' },
                { label: tr('static'), value: 'static' }
              ]}
            />
          </SettingRow>
          {shown.mode === 'static' && (
            <Panel className="space-y-3">
              <Input
                disabled={saving}
                value={shown.address}
                status={shown.address && invalid ? 'error' : undefined}
                placeholder={tr('addressPlaceholder')}
                addonBefore={tr('address')}
                onChange={(event) => setDraft({ ...shown, address: event.target.value })}
              />
              <Input
                disabled={saving}
                value={shown.gateway}
                placeholder={tr('gatewayPlaceholder')}
                addonBefore={tr('gateway')}
                onChange={(event) => setDraft({ ...shown, gateway: event.target.value })}
              />
              {invalid && shown.address && (
                <div className="text-danger text-xs">{tr('invalid')}</div>
              )}
              {dirty && (
                <div className="flex gap-2">
                  <Button
                    type="primary"
                    loading={saving}
                    disabled={invalid}
                    onClick={() =>
                      void apply({
                        enabled: true,
                        mode: 'static',
                        address: shown.address.trim(),
                        gateway: shown.gateway.trim()
                      })
                    }
                  >
                    {tr('save')}
                  </Button>
                  <Button disabled={saving} onClick={() => setDraft(undefined)}>
                    {tr('cancel')}
                  </Button>
                </div>
              )}
            </Panel>
          )}
          <Panel className="space-y-3">
            {addresses.length === 0 ? (
              <div className="text-fg-muted text-sm">{tr('waiting')}</div>
            ) : (
              addresses.map((address) => (
                <div key={address.address} className="space-y-1">
                  <div className="text-fg-muted flex items-center gap-2 text-xs">
                    <span>{tr(address.scope)}</span>
                    {address.source && (
                      <span>
                        ·{' '}
                        {address.source === 'static'
                          ? tr('static')
                          : address.source === 'dhcpv6'
                            ? 'DHCPv6'
                            : 'SLAAC'}
                      </span>
                    )}
                    {address.mtu > 0 && <span>· MTU {address.mtu}</span>}
                  </div>
                  <div className="text-fg font-mono text-sm break-all select-text">
                    {address.address}
                  </div>
                </div>
              ))
            )}
            <div className="text-fg-muted text-xs">{tr('disconnectHint')}</div>
          </Panel>
        </>
      )}
    </SettingsSection>
  );
};
