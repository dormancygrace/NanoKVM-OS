import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Checkbox, Input, InputNumber, Segmented, Switch } from 'antd';
import { CheckIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import type { EthernetConfig, EthernetMode } from '@/api/network.ts';
import {
  networkTrialDone,
  startNetworkTrial,
  type NetworkTrialResult
} from '@/lib/network-trial.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

import { IPv6 } from './ipv6.tsx';

function isIPv4(value: string) {
  const parts = value.trim().split('.');
  return (
    parts.length === 4 &&
    parts.every((part) => /^\d+$/.test(part) && Number(part) >= 0 && Number(part) <= 255)
  );
}

// "192.168.1.20/24": the address and the subnet mask of its prefix length
// (1-30, as the server accepts).
function parseCIDR(value: string) {
  const match = /^([^/]+)\/(\d{1,2})$/.exec(value.trim());
  const bits = match ? Number(match[2]) : -1;
  if (!match || !isIPv4(match[1]) || bits < 1 || bits > 30) return null;
  const mask = (0xffffffff << (32 - bits)) >>> 0;
  return {
    address: match[1].trim(),
    subnetMask: [24, 16, 8, 0].map((shift) => (mask >>> shift) & 255).join('.')
  };
}

export const Ethernet = () => {
  const { t } = useTranslation();
  const [config, setConfig] = useState<EthernetConfig>({
    enabled: true,
    adminUp: false,
    linkUp: false,
    mode: 'dhcp',
    interface: 'eth0',
    address: '',
    subnetMask: '255.255.255.0',
    gateway: '',
    vlanEnabled: false,
    vlanId: 0
  });
  const [original, setOriginal] = useState<EthernetConfig | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [isSaving, setIsSaving] = useState(false);
  const [message, setMessage] = useState('');
  const [loadError, setLoadError] = useState('');

  const getEthernet = useCallback(async () => {
    setIsLoading(true);
    setLoadError('');
    try {
      const rsp = await api.getEthernet();
      if (rsp.code !== 0) {
        setLoadError(rsp.msg || t('settings.network.ethernet.loadFailed'));
        return;
      }
      const fetched = rsp.data;
      setConfig(fetched);
      setOriginal(fetched);
    } catch {
      setLoadError(t('settings.network.ethernet.loadFailed'));
    } finally {
      setIsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void getEthernet();
  }, [getEthernet]);

  // The cable, the interface and the DHCP address change on their own; refresh
  // them while the panel is visible, leaving unsaved edits alone.
  // One refresh at a time: while the interface restarts, a request can hang.
  const refreshing = useRef(false);
  useEffect(() => {
    const timer = setInterval(async () => {
      if (document.visibilityState !== 'visible' || refreshing.current) return;
      refreshing.current = true;
      try {
        const rsp = await api.getEthernet();
        if (rsp.code !== 0) return;
        const { adminUp, linkUp, currentAddress } = rsp.data;
        const status = { adminUp, linkUp, currentAddress };
        setConfig((current) => ({ ...current, ...status }));
        setOriginal((current) => current && { ...current, ...status });
      } catch {
        // The next refresh tries again.
      } finally {
        refreshing.current = false;
      }
    }, 3000);
    return () => clearInterval(timer);
  }, []);

  function update(fields: Partial<EthernetConfig>) {
    setMessage('');
    setConfig((current) => ({ ...current, ...fields }));
  }

  // Link/address polling is runtime status, not an editable setting. In
  // particular, a save response may arrive after the next status refresh.
  const editableFields = [
    'enabled',
    'mode',
    'address',
    'subnetMask',
    'gateway',
    'vlanEnabled',
    'vlanId'
  ] as const;
  // Without the current settings there is nothing to change: Save would
  // send the defaults (DHCP, no VLAN) and could cut the device off.
  const hasChanges = !!original && editableFields.some((key) => config[key] !== original[key]);
  const cidr = config.mode === 'static' ? parseCIDR(config.address) : null;
  const invalidAddress = !isIPv4(config.address) && !cidr;
  // No gateway: a link without a router, such as a cable straight to a computer.
  const invalidGateway = config.gateway.trim() !== '' && !isIPv4(config.gateway);
  const invalidStatic = config.mode === 'static' && (invalidAddress || invalidGateway);
  const invalidVLAN =
    config.vlanEnabled &&
    (!Number.isInteger(config.vlanId) || config.vlanId < 1 || config.vlanId > 4094);

  async function save() {
    if (isSaving || invalidStatic || invalidVLAN) return;
    setIsSaving(true);
    setMessage('');
    try {
      const rsp = await api.setEthernet({
        enabled: config.enabled,
        mode: config.mode,
        address: config.mode === 'static' ? (cidr?.address ?? config.address.trim()) : '',
        subnetMask: config.mode === 'static' ? (cidr?.subnetMask ?? config.subnetMask.trim()) : '',
        gateway: config.mode === 'static' ? config.gateway.trim() : '',
        vlanEnabled: config.vlanEnabled,
        vlanId: config.vlanId
      });
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.ethernet.saveFailed');
        return;
      }
      const saved = cidr ? { ...config, ...cidr } : config;
      setConfig(saved);
      setOriginal(saved);
      // The device undoes the change unless the dialog reaches it.
      const pending = rsp.data?.trial;
      if (pending) startNetworkTrial(pending);
      else setMessage(t('settings.network.ethernet.saved'));
    } catch (err) {
      showRequestError(err, 'settings.network.ethernet.saveFailed');
    } finally {
      setIsSaving(false);
    }
  }

  // The check of a change on trial runs app-wide (NetworkTrialHost).
  useEffect(() => {
    const done = (event: Event) => {
      if ((event as CustomEvent<NetworkTrialResult>).detail === 'kept') {
        setMessage(t('settings.network.ethernet.saved'));
      } else {
        // The previous settings may be back: show what is in effect.
        setMessage('');
        void getEthernet();
      }
    };
    window.addEventListener(networkTrialDone, done);
    return () => window.removeEventListener(networkTrialDone, done);
  }, [getEthernet, t]);

  const linkState = !config.enabled ? (
    <StatusBadge tone="neutral">{t('settings.network.ethernet.disabled')}</StatusBadge>
  ) : !config.adminUp ? (
    <StatusBadge tone="warning">{t('settings.network.ethernet.interfaceDown')}</StatusBadge>
  ) : config.linkUp ? (
    <StatusBadge tone="success">
      {config.currentAddress
        ? t('settings.network.ethernet.linkUpAddress', { address: config.currentAddress })
        : t('settings.network.ethernet.linkUpNoAddress')}
    </StatusBadge>
  ) : (
    <StatusBadge tone="warning">{t('settings.network.ethernet.noCable')}</StatusBadge>
  );

  return (
    <div className="space-y-6">
      {loadError && (
        <Alert
          type="error"
          showIcon
          message={loadError}
          action={
            <Button size="small" loading={isLoading} onClick={() => void getEthernet()}>
              {t('settings.network.ethernet.retry')}
            </Button>
          }
        />
      )}

      <SettingsSection>
        <SettingRow
          label={t('settings.network.ethernet.enable')}
          description={linkState}
          htmlFor="ethernet-enabled"
        >
          <Switch
            id="ethernet-enabled"
            aria-label={t('settings.network.ethernet.enable')}
            aria-describedby="ethernet-enabled-description"
            checked={config.enabled}
            loading={isSaving}
            disabled={isLoading || isSaving || !original}
            onChange={(enabled) => update({ enabled })}
          />
        </SettingRow>
        {original?.enabled && !config.enabled && (
          <Alert
            type="warning"
            showIcon
            message={t('settings.network.ethernet.disconnectWarning')}
          />
        )}
      </SettingsSection>

      <SettingsSection
        title={t('settings.network.ethernet.ipv4')}
        description={
          config.mode === 'dhcp'
            ? t('settings.network.ethernet.dhcpDescription')
            : t('settings.network.ethernet.staticDescription')
        }
      >
        <SettingRow
          label={t('settings.network.ethernet.title')}
          description={t('settings.network.ethernet.description')}
        >
          <Segmented
            disabled={isLoading || isSaving}
            value={config.mode}
            onChange={(mode) => update({ mode: mode as EthernetMode })}
            options={[
              { label: t('settings.network.ethernet.dhcp'), value: 'dhcp' },
              { label: t('settings.network.ethernet.static'), value: 'static' }
            ]}
          />
        </SettingRow>

        <SettingRow
          label={
            <Checkbox
              checked={config.vlanEnabled}
              disabled={isLoading || isSaving}
              onChange={(event) => update({ vlanEnabled: event.target.checked })}
            >
              {t('settings.network.ethernet.vlan')}
            </Checkbox>
          }
          description={t('settings.network.ethernet.vlanDescription')}
        >
          {config.vlanEnabled && (
            <InputNumber
              min={1}
              max={4094}
              style={{ width: 180 }}
              value={config.vlanId || undefined}
              placeholder={t('settings.network.ethernet.vlanId')}
              status={invalidVLAN ? 'error' : undefined}
              disabled={isLoading || isSaving}
              onChange={(vlanId) => update({ vlanId: vlanId ?? 0 })}
            />
          )}
        </SettingRow>
        {invalidVLAN && (
          <div className="text-danger text-xs">{t('settings.network.ethernet.invalidVlan')}</div>
        )}
        {original &&
          (config.vlanEnabled !== original.vlanEnabled ||
            (config.vlanEnabled && config.vlanId !== original.vlanId)) && (
            <Alert type="warning" showIcon message={t('settings.network.ethernet.vlanWarning')} />
          )}

        {config.mode === 'static' && (
          <Panel className="space-y-3">
            <Input
              disabled={isLoading || isSaving}
              value={config.address}
              status={config.address && invalidAddress ? 'error' : undefined}
              placeholder={t('settings.network.ethernet.addressPlaceholder')}
              addonBefore={t('settings.network.ethernet.ipAddress')}
              onChange={(event) => update({ address: event.target.value })}
            />
            <Input
              value={cidr ? cidr.subnetMask : config.subnetMask}
              disabled={isLoading || isSaving || !!cidr}
              placeholder={t('settings.network.ethernet.subnetMaskPlaceholder')}
              addonBefore={t('settings.network.ethernet.subnetMask')}
              onChange={(event) => update({ subnetMask: event.target.value })}
            />
            <Input
              disabled={isLoading || isSaving}
              value={config.gateway}
              status={invalidGateway ? 'error' : undefined}
              placeholder={t('settings.network.ethernet.gatewayPlaceholder')}
              addonBefore={t('settings.network.ethernet.gateway')}
              onChange={(event) => update({ gateway: event.target.value })}
            />
            {invalidStatic && (
              <div className="text-danger text-xs">{t('settings.network.ethernet.invalid')}</div>
            )}
          </Panel>
        )}
      </SettingsSection>

      {(hasChanges || message) && (
        <div className="flex items-center justify-between gap-4">
          <span className={`text-xs ${message ? 'text-success' : 'text-warning'}`}>
            {message || t('settings.network.ethernet.unsaved')}
          </span>
          <Button
            type={hasChanges ? 'primary' : 'default'}
            icon={message ? <CheckIcon size={14} /> : undefined}
            loading={isSaving}
            disabled={isLoading || !original || !hasChanges || invalidStatic || invalidVLAN}
            onClick={save}
          >
            {t('settings.network.ethernet.save')}
          </Button>
        </div>
      )}

      {/* Applied at once, unlike the IPv4 settings above and their Save. */}
      <IPv6 name="eth0" />
    </div>
  );
};
