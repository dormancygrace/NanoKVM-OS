import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Spin, Tabs } from 'antd';
import { useAtomValue } from 'jotai';
import { DownloadIcon, RefreshCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { downloadDiagnosticsReport, getDiagnostics } from '@/api/vm';
import { formatDeviceTime } from '@/lib/date-time.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { timePreferencesAtom } from '@/hooks/useDeviceTime.ts';
import { Panel, StatusBadge } from '@/components/ui/settings.tsx';

import { Logs } from './logs';

type Item = { state: string; detail?: string; optional?: boolean };
type Service = Item & { name: string };
type Snapshot = {
  collectedAt: number;
  versions: {
    application?: string;
    image?: string;
    alpine?: string;
    kernel?: string;
    systemBase?: string;
    modules: Item;
    boot: Item;
    packages: { name: string; version?: string; state: string }[];
  };
  services: Service[];
  video: {
    enabled: boolean;
    signal: boolean;
    profile?: string;
    input?: string;
    output?: string;
    measuredFps?: string;
    edidState: string;
    state: string;
  };
  usb: { selected: string[]; binding: Item };
  apk: {
    state: string;
    errorCategory?: string;
  };
  firewall: {
    state: string;
    detail?: string;
    hookChains: { label: string; hook: string; policy?: string }[];
  };
};

const dash = (value?: string | null) => value || '—';

const DiagnosticsStatus = () => {
  const { t, i18n } = useTranslation();
  const timePreferences = useAtomValue(timePreferencesAtom);
  const [snapshot, setSnapshot] = useState<Snapshot>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [downloading, setDownloading] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const response = await getDiagnostics();
      if (response.code !== 0)
        throw new Error(response.msg || t('settings.system.diagnostics.loadError'));
      setSnapshot(response.data as Snapshot);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t('settings.system.diagnostics.loadError'));
    } finally {
      setLoading(false);
    }
  }, [t]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const download = async () => {
    setDownloading(true);
    try {
      const response = (await downloadDiagnosticsReport()) as unknown as Blob;
      const url = URL.createObjectURL(new Blob([response], { type: 'application/json' }));
      const link = document.createElement('a');
      link.href = url;
      link.download = 'nanokvm-diagnostics.json';
      link.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      showRequestError(err, 'settings.system.diagnostics.downloadError');
    } finally {
      setDownloading(false);
    }
  };

  const state = (item?: Item) => {
    const key = item?.state || 'unavailable';
    return (
      <StatusBadge
        tone={
          key === 'error' ? 'danger' : key === 'running' || key === 'ok' ? 'success' : 'warning'
        }
      >
        {t(`settings.system.diagnostics.states.${key}`, { defaultValue: key })}
      </StatusBadge>
    );
  };
  const line = (label: string, value?: string | null) => (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-3 py-1 text-sm">
      <span className="text-fg-muted min-w-0">{label}</span>
      <span className="min-w-0 text-right break-words">{dash(value)}</span>
    </div>
  );
  const card = (title: string, children: React.ReactNode) => (
    <Panel className="min-w-0">
      <h3 className="m-0 mb-2 text-sm font-medium">{title}</h3>
      {children}
    </Panel>
  );

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p className="text-fg-muted mb-0 text-sm">
            {t('settings.system.diagnostics.description')}
          </p>
        </div>
        <div className="flex gap-2">
          <Button
            size="small"
            icon={<RefreshCwIcon size={14} />}
            loading={loading}
            onClick={() => void refresh()}
          >
            {t('settings.system.diagnostics.refresh')}
          </Button>
          <Button
            size="small"
            icon={<DownloadIcon size={14} />}
            loading={downloading}
            onClick={() => void download()}
          >
            {t('settings.system.diagnostics.download')}
          </Button>
        </div>
      </div>
      {error && <Alert type="error" showIcon message={error} />}
      {loading && !snapshot ? (
        <div className="flex justify-center py-10">
          <Spin />
        </div>
      ) : (
        snapshot && (
          <>
            <p className="text-fg-muted text-xs">
              {t('settings.system.diagnostics.collected', {
                time: formatDeviceTime(snapshot.collectedAt, timePreferences, i18n.language, true)
              })}
            </p>
            <div className="grid gap-4 lg:grid-cols-2">
              {card(
                t('settings.system.diagnostics.versions'),
                <>
                  {line(
                    t('settings.system.diagnostics.application'),
                    snapshot.versions.application
                  )}
                  {line(t('settings.system.diagnostics.image'), snapshot.versions.image)}
                  {line(t('settings.system.diagnostics.alpine'), snapshot.versions.alpine)}
                  {line(t('settings.system.diagnostics.kernel'), snapshot.versions.kernel)}
                  {line(t('settings.system.diagnostics.systemBase'), snapshot.versions.systemBase)}
                  <div className="pt-1 text-sm">
                    {t('settings.system.diagnostics.modules')}: {state(snapshot.versions.modules)}
                  </div>
                  <p className="text-fg-muted mb-0 text-xs">{snapshot.versions.modules.detail}</p>
                  <div className="pt-2 text-sm">
                    {t('settings.system.diagnostics.boot')}: {state(snapshot.versions.boot)}
                  </div>
                  <p className="text-fg-muted mb-0 text-xs">{snapshot.versions.boot.detail}</p>
                  {snapshot.versions.packages.map((pkg) =>
                    line(
                      pkg.name,
                      pkg.version ||
                        t(`settings.system.diagnostics.states.${pkg.state}`, {
                          defaultValue: pkg.state
                        })
                    )
                  )}
                </>
              )}
              {card(
                t('settings.system.diagnostics.video'),
                <>
                  <div className="text-sm">
                    {t('settings.system.diagnostics.capture')}:{' '}
                    {state({ state: snapshot.video.state })}
                  </div>
                  <div className="text-sm">
                    {t('settings.system.diagnostics.edid')}:{' '}
                    {state({ state: snapshot.video.edidState })}
                  </div>
                  {line(t('settings.system.diagnostics.profile'), snapshot.video.profile)}
                  {line(t('settings.system.diagnostics.input'), snapshot.video.input)}
                  {line(t('settings.system.diagnostics.output'), snapshot.video.output)}
                  {line(t('settings.system.diagnostics.fps'), snapshot.video.measuredFps)}
                </>
              )}
              {card(
                t('settings.system.diagnostics.usb'),
                <>
                  <div className="text-sm">
                    {t('settings.system.diagnostics.binding')}: {state(snapshot.usb.binding)}
                  </div>
                  <p className="text-fg-muted mb-2 text-xs">{snapshot.usb.binding.detail}</p>
                  {line(
                    t('settings.system.diagnostics.selected'),
                    snapshot.usb.selected.join(', ')
                  )}
                </>
              )}
              {card(
                t('settings.system.diagnostics.firewall'),
                <>
                  <div className="text-sm">{state({ state: snapshot.firewall.state })}</div>
                  <p className="text-fg-muted text-xs">{snapshot.firewall.detail}</p>
                  {snapshot.firewall.hookChains.map((chain) => (
                    <div key={chain.label} className="border-line border-t py-1 text-xs">
                      {chain.label}: {chain.hook}
                      {chain.policy ? ` (${chain.policy})` : ''}
                    </div>
                  ))}
                </>
              )}
            </div>
            {card(
              t('settings.system.diagnostics.services'),
              <div className="divide-line divide-y">
                {snapshot.services.map((service) => (
                  <div key={service.name} className="py-3 first:pt-0 last:pb-0">
                    <div className="flex justify-between gap-3 text-sm">
                      <span>
                        {service.name}
                        {service.optional ? ` · ${t('settings.system.diagnostics.optional')}` : ''}
                      </span>
                      {state(service)}
                    </div>
                    <p className="text-fg-muted mb-0 text-xs">{service.detail}</p>
                  </div>
                ))}
              </div>
            )}
            {card(
              t('settings.system.diagnostics.apk'),
              <>
                <div className="text-sm">
                  {t('settings.system.diagnostics.apk')}:{' '}
                  {state({
                    state:
                      snapshot.apk.state === 'failed'
                        ? 'error'
                        : snapshot.apk.state === 'idle'
                          ? 'ok'
                          : snapshot.apk.state
                  })}
                </div>
                {snapshot.apk.errorCategory && (
                  <p className="text-fg-muted mb-1 text-xs">
                    {t('settings.system.diagnostics.operationFailed')}
                  </p>
                )}
              </>
            )}
          </>
        )
      )}
    </div>
  );
};

export const Diagnostics = () => {
  const { t } = useTranslation();
  return (
    <div className="min-w-0 space-y-6">
      <Tabs
        defaultActiveKey="status"
        destroyOnHidden
        items={[
          {
            key: 'status',
            label: t('settings.system.diagnostics.overview'),
            children: <DiagnosticsStatus />
          },
          {
            key: 'logs',
            label: t('settings.system.logs.title'),
            children: <Logs />
          }
        ]}
      />
    </div>
  );
};
