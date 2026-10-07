import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, Segmented } from 'antd';
import { CheckIcon, ChevronDownIcon, PlusIcon, XIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import type { DNSMode } from '@/api/network.ts';
import { requestErrorText } from '@/lib/request-error.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

type DNSState = {
  mode: DNSMode;
  servers: string[];
  dhcp: string[];
  info: DNSInfo;
};

type DNSInfo = {
  interface?: string;
  type?: string;
  address?: string;
  subnetMask?: string;
  gateway?: string;
};

const maxServers = 6;

function formatInterface(info: DNSInfo) {
  if (!info.interface) return '';
  if (!info.type) return info.interface;

  return `${info.type} (${info.interface})`;
}

function normalizeServers(servers: string[]) {
  const seen = new Set<string>();
  const normalized: string[] = [];

  for (const server of servers) {
    const value = normalizeServer(server);
    if (!value || seen.has(value)) continue;

    seen.add(value);
    normalized.push(value);
  }

  return normalized;
}

function normalizeServer(server: string) {
  return server.split('#')[0].trim().split(/\s+/)[0] || '';
}

function isValidIP(value: string) {
  return isValidIPv4(value) || isValidIPv6(value);
}

function isValidIPv4(value: string) {
  const parts = value.split('.');
  if (parts.length !== 4) return false;

  return parts.every((part) => {
    if (!/^\d+$/.test(part)) return false;
    if (part.length > 1 && part.startsWith('0')) return false;

    const number = Number(part);
    return number >= 0 && number <= 255;
  });
}

function isValidIPv6(value: string) {
  if (!value.includes(':')) return false;

  try {
    new URL(`http://[${value}]/`);
    return true;
  } catch {
    return false;
  }
}

const InfoRow = ({ label, value }: { label: string; value?: string }) => {
  return (
    <div className="flex min-h-[44px] items-center justify-between gap-4">
      <span className="text-sm">{label}</span>
      <span className="text-fg-muted max-w-[330px] text-right text-sm break-all">
        {value || '-'}
      </span>
    </div>
  );
};

const ServerList = ({ servers }: { servers: string[] }) => {
  const { t } = useTranslation();

  if (!servers.length) {
    return (
      <Panel>
        <div className="text-fg-muted text-sm">{t('settings.network.dns.none')}</div>
      </Panel>
    );
  }

  return (
    <Panel flush className="divide-line divide-y">
      {servers.map((server, index) => (
        <div key={`${server}-${index}`} className="text-fg-muted px-4 py-3 text-sm">
          {server}
        </div>
      ))}
    </Panel>
  );
};

const EditableServerRow = ({
  value,
  label,
  removeLabel,
  autoFocus,
  onChange,
  onRemove
}: {
  value: string;
  label: string;
  removeLabel: string;
  autoFocus: boolean;
  onChange: (value: string) => void;
  onRemove: () => void;
}) => {
  const inputRef = useRef<any>(null);
  const normalized = normalizeServer(value);
  const isInvalid = normalized !== '' && !isValidIP(normalized);

  useEffect(() => {
    if (autoFocus && inputRef.current) {
      inputRef.current.focus();
    }
  }, [autoFocus]);

  return (
    <div className="group flex items-center gap-2">
      <Input
        ref={inputRef}
        aria-label={label}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="0.0.0.0"
        status={isInvalid ? 'error' : undefined}
      />
      <Button
        size="small"
        shape="circle"
        aria-label={removeLabel}
        title={removeLabel}
        icon={<XIcon size={14} />}
        onClick={onRemove}
        className="opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100"
      />
    </div>
  );
};

export const DNS = () => {
  const { t } = useTranslation();

  const [mode, setMode] = useState<DNSMode>('dhcp');
  const [originalMode, setOriginalMode] = useState<DNSMode>('dhcp');
  const [servers, setServers] = useState<string[]>([]);
  const [originalServers, setOriginalServers] = useState<string[]>([]);
  const [dhcp, setDHCP] = useState<string[]>([]);
  const [info, setInfo] = useState<DNSInfo>({});

  const [isLoading, setIsLoading] = useState(false);
  const [isSaving, setIsSaving] = useState(false);
  // The failed load request; its text is derived when rendering.
  const [loadError, setLoadError] = useState<{ error: unknown } | null>(null);
  const [message, setMessage] = useState('');
  // Validation of the entered servers; request failures are shown as toasts.
  const [error, setError] = useState('');
  const [focusNewRow, setFocusNewRow] = useState(false);

  useEffect(() => {
    getDNS();
  }, []);

  async function getDNS(showLoading = true) {
    if (showLoading) setIsLoading(true);

    try {
      const rsp = await api.getDNS();
      if (rsp.code !== 0) {
        setLoadError({ error: rsp });
        return;
      }

      const data = rsp.data as DNSState;
      const fetchedMode = data.mode || 'dhcp';
      setMode(fetchedMode);
      setOriginalMode(fetchedMode);

      const fetchedServers = data.servers?.filter(Boolean) || [];
      setServers(fetchedServers);
      setOriginalServers(fetchedServers);
      setDHCP(data.dhcp || []);
      setInfo(data.info || {});
      setLoadError(null);
    } catch (err) {
      setLoadError({ error: err });
    } finally {
      if (showLoading) setIsLoading(false);
    }
  }

  async function save() {
    if (isSaving) return;

    setMessage('');
    setError('');

    const normalized = normalizeServers(servers);
    if (mode === 'manual' && normalized.length === 0) {
      setError(t('settings.network.dns.invalid'));
      return;
    }

    if (mode === 'manual' && normalized.some((server) => !isValidIP(server))) {
      setError(t('settings.network.dns.invalid'));
      return;
    }

    setIsSaving(true);
    try {
      const rsp = await api.setDNS(mode, mode === 'manual' ? normalized : []);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.dns.saveFailed');
        return;
      }

      setServers(normalized);
      setOriginalServers(normalized);
      setOriginalMode(mode);

      await getDNS(false);
      setMessage(t('settings.network.dns.saved'));
    } catch (err) {
      showRequestError(err, 'settings.network.dns.saveFailed');
    } finally {
      setIsSaving(false);
    }
  }

  function addServer() {
    if (servers.length >= maxServers) return;
    setMessage('');
    setError('');
    setServers([...servers, '']);
    setFocusNewRow(true);
  }

  function removeServer(index: number) {
    setMessage('');
    setError('');
    setServers(servers.filter((_, i) => i !== index));
  }

  function updateServer(index: number, value: string) {
    setMessage('');
    setError('');
    const updated = [...servers];
    updated[index] = value;
    setServers(updated);
  }

  const normalizedServers = normalizeServers(servers);
  const hasInvalidServer =
    mode === 'manual' &&
    servers.some((server) => {
      const val = normalizeServer(server);
      return val !== '' && !isValidIP(val);
    });
  const isExceedMax = mode === 'manual' && normalizedServers.length > maxServers;
  const hasChanges =
    mode !== originalMode ||
    normalizedServers.join(',') !== normalizeServers(originalServers).join(',');

  const statusText = error || message || (hasChanges ? t('settings.network.dns.unsaved') : '');
  const statusColor = error ? 'text-danger' : message ? 'text-success' : 'text-warning';
  const serversDescription =
    mode === 'dhcp'
      ? t('settings.network.dns.dhcpServersDescription')
      : t('settings.network.dns.manualServersDescription');

  const loadErrorText = loadError && requestErrorText(loadError.error, t('error.requestFailed'));

  const canAdd = !isLoading && !isSaving && servers.length < maxServers;

  return (
    <SettingsSection>
      {loadErrorText && <Alert type="error" showIcon message={loadErrorText} />}

      <SettingRow
        label={t('settings.network.dns.title')}
        description={t('settings.network.dns.description')}
      >
        <Segmented
          disabled={isLoading || isSaving}
          value={mode}
          onChange={(val) => {
            setMode(val as DNSMode);
            setMessage('');
            setError('');
          }}
          options={[
            { label: t('settings.network.dns.dhcp'), value: 'dhcp' },
            { label: t('settings.network.dns.manual'), value: 'manual' }
          ]}
        />
      </SettingRow>

      <SettingRow
        label={t('settings.network.dns.dnsServers')}
        description={serversDescription}
        stacked
      >
        {mode === 'manual' ? (
          <Panel className="space-y-2">
            {servers.length === 0 ? (
              <div className="text-fg-muted text-sm">{t('settings.network.dns.none')}</div>
            ) : (
              servers.map((server, index) => (
                <EditableServerRow
                  key={index}
                  value={server}
                  label={t('settings.network.dns.server', { index: index + 1 })}
                  removeLabel={t('settings.network.dns.remove', { index: index + 1 })}
                  autoFocus={focusNewRow && index === servers.length - 1}
                  onChange={(val) => updateServer(index, val)}
                  onRemove={() => removeServer(index)}
                />
              ))
            )}

            {/* Add server button */}
            {canAdd && (
              <div className="flex items-center gap-2">
                <Button
                  type="dashed"
                  className="flex-1"
                  icon={<PlusIcon size={14} />}
                  onClick={addServer}
                >
                  {t('settings.network.dns.add')}
                </Button>
                <Button
                  type="text"
                  size="small"
                  className="invisible shrink-0"
                  icon={<XIcon size={14} />}
                />
              </div>
            )}

            {/* Validation hints */}
            {(hasInvalidServer || isExceedMax) && (
              <div className="space-y-1">
                {hasInvalidServer && (
                  <div className="text-danger text-xs">{t('settings.network.dns.invalid')}</div>
                )}
                {isExceedMax && (
                  <div className="text-danger text-xs">
                    {t('settings.network.dns.maxServers', { count: maxServers })}
                  </div>
                )}
              </div>
            )}
          </Panel>
        ) : (
          <ServerList servers={dhcp} />
        )}
      </SettingRow>

      {/* Footer: status + save button */}
      {(hasChanges || statusText) && (
        <div className="flex items-center justify-between gap-4">
          <span className={`text-xs ${statusColor}`}>{statusText}</span>

          <Button
            type={hasChanges ? 'primary' : 'default'}
            icon={message ? <CheckIcon size={14} /> : undefined}
            loading={isSaving}
            disabled={
              isLoading || (!hasChanges && !hasInvalidServer) || hasInvalidServer || isExceedMax
            }
            onClick={save}
          >
            {t('settings.network.dns.save')}
          </Button>
        </div>
      )}

      <Panel flush className="overflow-hidden">
        <details className="group">
          <summary className="text-fg-muted hover:text-fg flex cursor-pointer list-none items-center justify-between px-4 py-3 text-sm [&::-webkit-details-marker]:hidden">
            <span>{t('settings.network.dns.networkDetails')}</span>
            <ChevronDownIcon size={16} className="transition-transform group-open:rotate-180" />
          </summary>
          <div className="divide-line divide-y px-4">
            <InfoRow label={t('settings.network.dns.interface')} value={formatInterface(info)} />
            <InfoRow label={t('settings.network.dns.ipAddress')} value={info.address} />
            <InfoRow label={t('settings.network.dns.subnetMask')} value={info.subnetMask} />
            <InfoRow label={t('settings.network.dns.router')} value={info.gateway} />
          </div>
        </details>
      </Panel>
    </SettingsSection>
  );
};
