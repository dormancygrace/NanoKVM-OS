import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Checkbox, Input, InputNumber, Segmented, Switch } from 'antd';
import { CheckIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import type { EthernetConfig, EthernetMode } from '@/api/network.ts';

function isIPv4(value: string) {
  const parts = value.trim().split('.');
  return (
    parts.length === 4 &&
    parts.every((part) => /^\d+$/.test(part) && Number(part) >= 0 && Number(part) <= 255)
  );
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
  const [error, setError] = useState('');

  const getEthernet = useCallback(async () => {
    setIsLoading(true);
    try {
      const rsp = await api.getEthernet();
      if (rsp.code !== 0) {
        setError(rsp.msg || t('settings.network.ethernet.loadFailed'));
        return;
      }
      const fetched = rsp.data as EthernetConfig;
      setConfig(fetched);
      setOriginal(fetched);
    } catch {
      setError(t('settings.network.ethernet.loadFailed'));
    } finally {
      setIsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void getEthernet();
  }, [getEthernet]);

  function update(fields: Partial<EthernetConfig>) {
    setMessage('');
    setError('');
    setConfig((current) => ({ ...current, ...fields }));
  }

  const hasChanges = JSON.stringify(config) !== JSON.stringify(original);
  const invalidStatic =
    config.mode === 'static' && (!isIPv4(config.address) || !isIPv4(config.gateway));
  const invalidVLAN =
    config.vlanEnabled &&
    (!Number.isInteger(config.vlanId) || config.vlanId < 1 || config.vlanId > 4094);

  async function save() {
    if (isSaving || invalidStatic || invalidVLAN) return;
    setIsSaving(true);
    setMessage('');
    setError('');
    try {
      const rsp = await api.setEthernet({
        enabled: config.enabled,
        mode: config.mode,
        address: config.mode === 'static' ? config.address.trim() : '',
        subnetMask: config.mode === 'static' ? config.subnetMask.trim() : '',
        gateway: config.mode === 'static' ? config.gateway.trim() : '',
        vlanEnabled: config.vlanEnabled,
        vlanId: config.vlanId
      });
      if (rsp.code !== 0) {
        setError(rsp.msg || t('settings.network.ethernet.saveFailed'));
        return;
      }
      setOriginal(config);
      setMessage(t('settings.network.ethernet.saved'));
    } catch {
      setError(t('settings.network.ethernet.saveFailed'));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <div className="flex flex-col space-y-5">
      <div className="flex items-center justify-between">
        <div className="flex flex-col space-y-1">
          <span>{t('settings.network.ethernet.enable')}</span>
          <span className="text-xs text-neutral-500">
            {!config.enabled
              ? t('settings.network.ethernet.disabled')
              : !config.adminUp
                ? t('settings.network.ethernet.interfaceDown')
                : config.linkUp
                  ? t('settings.network.ethernet.linkUp')
                  : t('settings.network.ethernet.noCable')}
          </span>
        </div>
        <Switch
          aria-label={t('settings.network.ethernet.enable')}
          checked={config.enabled}
          loading={isSaving}
          disabled={isLoading || isSaving}
          onChange={(enabled) => update({ enabled })}
        />
      </div>

      {!config.enabled && (
        <Alert type="warning" showIcon message={t('settings.network.ethernet.disconnectWarning')} />
      )}
      {original &&
        (config.vlanEnabled !== original.vlanEnabled ||
          (config.vlanEnabled && config.vlanId !== original.vlanId)) && (
          <Alert type="warning" showIcon message={t('settings.network.ethernet.vlanWarning')} />
        )}

      <div className="flex items-center justify-between gap-4">
        <div className="flex flex-col space-y-1">
          <span>{t('settings.network.ethernet.title')}</span>
          <span className="text-xs text-neutral-500">
            {t('settings.network.ethernet.description')}
          </span>
        </div>
        <Segmented
          disabled={isLoading || isSaving}
          value={config.mode}
          onChange={(mode) => update({ mode: mode as EthernetMode })}
          options={[
            { label: t('settings.network.ethernet.dhcp'), value: 'dhcp' },
            { label: t('settings.network.ethernet.static'), value: 'static' }
          ]}
        />
      </div>

      <div className="flex items-center justify-between gap-3 rounded-xl bg-neutral-800/50 p-4">
        <div>
          <Checkbox
            checked={config.vlanEnabled}
            disabled={isLoading || isSaving}
            onChange={(event) => update({ vlanEnabled: event.target.checked })}
          >
            {t('settings.network.ethernet.vlan')}
          </Checkbox>
          <div className="text-xs text-neutral-500">
            {t('settings.network.ethernet.vlanDescription')}
          </div>
        </div>
        {config.vlanEnabled && (
          <InputNumber
            min={1}
            max={4094}
            value={config.vlanId || undefined}
            placeholder={t('settings.network.ethernet.vlanId')}
            status={invalidVLAN ? 'error' : undefined}
            disabled={isLoading || isSaving}
            onChange={(vlanId) => update({ vlanId: vlanId ?? 0 })}
          />
        )}
      </div>
      {invalidVLAN && (
        <div className="text-xs text-red-400">{t('settings.network.ethernet.invalidVlan')}</div>
      )}

      <div className="overflow-hidden rounded-xl bg-neutral-800/50">
        <div className="px-4 pt-3 pb-1.5">
          <div className="font-semibold text-neutral-100">
            {t('settings.network.ethernet.ipv4')}
          </div>
          <div className="mt-0.5 text-xs leading-snug text-neutral-500">
            {config.mode === 'dhcp'
              ? t('settings.network.ethernet.dhcpDescription')
              : t('settings.network.ethernet.staticDescription')}
          </div>
        </div>

        {config.mode === 'static' && (
          <div className="space-y-3 px-4 pt-2 pb-4">
            <Input
              value={config.address}
              status={config.address && !isIPv4(config.address) ? 'error' : undefined}
              placeholder={t('settings.network.ethernet.addressPlaceholder')}
              addonBefore={t('settings.network.ethernet.ipAddress')}
              onChange={(event) => update({ address: event.target.value })}
            />
            <Input
              value={config.subnetMask}
              placeholder={t('settings.network.ethernet.subnetMaskPlaceholder')}
              addonBefore={t('settings.network.ethernet.subnetMask')}
              onChange={(event) => update({ subnetMask: event.target.value })}
            />
            <Input
              value={config.gateway}
              status={config.gateway && !isIPv4(config.gateway) ? 'error' : undefined}
              placeholder={t('settings.network.ethernet.gatewayPlaceholder')}
              addonBefore={t('settings.network.ethernet.gateway')}
              onChange={(event) => update({ gateway: event.target.value })}
            />
            {invalidStatic && (
              <div className="text-xs text-red-400">{t('settings.network.ethernet.invalid')}</div>
            )}
          </div>
        )}
      </div>

      {(hasChanges || message || error) && (
        <div className="flex items-center justify-between gap-4">
          <span
            className={`text-xs ${error ? 'text-red-400' : message ? 'text-green-400' : 'text-yellow-400/80'}`}
          >
            {error || message || t('settings.network.ethernet.unsaved')}
          </span>
          <Button
            type={hasChanges ? 'primary' : 'default'}
            icon={message ? <CheckIcon size={14} /> : undefined}
            loading={isSaving}
            disabled={isLoading || !hasChanges || invalidStatic || invalidVLAN}
            onClick={save}
          >
            {t('settings.network.ethernet.save')}
          </Button>
        </div>
      )}
    </div>
  );
};
