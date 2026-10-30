import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { Alert, Button, Spin, Tabs } from 'antd';
import { useAtomValue } from 'jotai';
import { ChevronDownIcon, DownloadIcon, RefreshCwIcon } from 'lucide-react';
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
    hookChains: { label: string; hook: string; policy?: string; feature?: string }[];
  };
};

const dash = (value?: string | null) => value || '—';
const good = (state?: string) =>
  state === 'ok' || state === 'running' || state === 'up-to-date' || state === 'installed';
// States that describe a choice or a normal condition, not a fault.
// "no signal" is usually the host PC being off; "disabled" is the user's own switch.
const neutralStates = ['saved-profile', 'configured', 'ready', 'no-signal', 'disabled'];

// A problem the summary lists: a label, its state and why.
type Problem = { label: string; item: Item };

// What is wrong, from the snapshot alone. Optional services that are not
// running and a USB gadget switched off on purpose are not problems.
function diagnosticProblems(snapshot: Snapshot, label: (key: string) => string): Problem[] {
  const problems: Problem[] = [];
  const check = (name: string, item: Item) => {
    if (!good(item.state) && !neutralStates.includes(item.state))
      problems.push({ label: name, item });
  };
  check(label('modules'), snapshot.versions.modules);
  check(label('boot'), snapshot.versions.boot);
  if (snapshot.video.enabled) check(label('capture'), { state: snapshot.video.state });
  if (['error', 'power-cycle-pending'].includes(snapshot.video.edidState))
    problems.push({ label: label('edid'), item: { state: snapshot.video.edidState } });
  if (snapshot.usb.selected.length > 0) check(label('binding'), snapshot.usb.binding);
  if (snapshot.firewall.state === 'error')
    problems.push({ label: label('firewall'), item: snapshot.firewall });
  for (const service of snapshot.services)
    if (service.optional ? service.state === 'error' : !good(service.state))
      problems.push({ label: service.name, item: service });
  if (snapshot.apk.state === 'error' || snapshot.apk.state === 'failed')
    problems.push({
      label: label('apk'),
      item: { state: 'error', detail: snapshot.apk.errorCategory && label('operationFailed') }
    });
  return problems;
}

const DiagnosticsStatus = () => {
  const { t, i18n } = useTranslation();
  const tr = useCallback((key: string) => t(`settings.system.diagnostics.${key}`), [t]);
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
      const response = await downloadDiagnosticsReport();
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

  // neutral: a state that is a choice, such as USB switched off.
  const state = (item?: Item, neutral = false) => {
    const key = item?.state || 'unavailable';
    return (
      <StatusBadge
        tone={
          key === 'error'
            ? 'danger'
            : good(key)
              ? 'success'
              : neutral || neutralStates.includes(key)
                ? 'neutral'
                : 'warning'
        }
      >
        {t(`settings.system.diagnostics.states.${key}`, { defaultValue: key })}
      </StatusBadge>
    );
  };
  // Explanations only where something is not right.
  const detail = (item: Item, neutral = false) =>
    !good(item.state) &&
    !neutral &&
    item.detail && <p className="text-fg-muted mb-0 text-xs">{item.detail}</p>;
  const line = (label: string, value?: string | null) => (
    <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-3 py-1 text-sm">
      <span className="text-fg-muted min-w-0">{label}</span>
      <span className="min-w-0 text-right break-words">{dash(value)}</span>
    </div>
  );
  const card = (title: string, children: ReactNode) => (
    <Panel className="min-w-0">
      <h3 className="m-0 mb-2 text-sm font-medium">{title}</h3>
      {children}
    </Panel>
  );
  const fold = (title: string, children: ReactNode) => (
    <details className="group mt-2">
      <summary className="text-fg-muted hover:text-fg flex cursor-pointer list-none items-center justify-between gap-3 py-1 text-sm [&::-webkit-details-marker]:hidden">
        <span>{title}</span>
        <ChevronDownIcon size={16} className="transition-transform group-open:rotate-180" />
      </summary>
      {children}
    </details>
  );

  const problems = snapshot ? diagnosticProblems(snapshot, tr) : [];
  const usbOff = !!snapshot && snapshot.usb.selected.length === 0;
  const running = snapshot?.services.filter((service) => good(service.state)) ?? [];
  const optionalIdle =
    snapshot?.services.filter(
      (service) => service.optional && !good(service.state) && service.state !== 'error'
    ) ?? [];
  const failing =
    snapshot?.services.filter(
      (service) => !good(service.state) && !optionalIdle.includes(service)
    ) ?? [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <p className="text-fg-muted mb-0 text-sm">{tr('description')}</p>
        </div>
        <div className="flex gap-2">
          <Button
            size="small"
            icon={<RefreshCwIcon size={14} />}
            loading={loading}
            onClick={() => void refresh()}
          >
            {tr('refresh')}
          </Button>
          <Button
            size="small"
            icon={<DownloadIcon size={14} />}
            loading={downloading}
            onClick={() => void download()}
          >
            {tr('download')}
          </Button>
        </div>
      </div>
      {error && <Alert type="error" showIcon title={error} />}
      {loading && !snapshot ? (
        <div className="flex justify-center py-10">
          <Spin />
        </div>
      ) : (
        snapshot && (
          <>
            {problems.length === 0 ? (
              <Alert type="success" showIcon title={tr('allOk')} />
            ) : (
              <Alert
                type="warning"
                showIcon
                title={t('settings.system.diagnostics.problems', { count: problems.length })}
                description={
                  <ul className="m-0 ps-4">
                    {problems.map((problem) => (
                      <li key={problem.label}>
                        {problem.label}:{' '}
                        {t(`settings.system.diagnostics.states.${problem.item.state}`, {
                          defaultValue: problem.item.state
                        })}
                        {problem.item.detail ? ` — ${problem.item.detail}` : ''}
                      </li>
                    ))}
                  </ul>
                }
              />
            )}
            <p className="text-fg-muted text-xs">
              {t('settings.system.diagnostics.collected', {
                time: formatDeviceTime(snapshot.collectedAt, timePreferences, i18n.language, true)
              })}
            </p>
            <div className="grid gap-4 lg:grid-cols-2">
              {card(
                tr('versions'),
                <>
                  {line(tr('application'), snapshot.versions.application)}
                  {line(tr('image'), snapshot.versions.image)}
                  {line(tr('alpine'), snapshot.versions.alpine)}
                  {line(tr('kernel'), snapshot.versions.kernel)}
                  <div className="pt-1 text-sm">
                    {tr('modules')}: {state(snapshot.versions.modules)}
                  </div>
                  {detail(snapshot.versions.modules)}
                  <div className="pt-2 text-sm">
                    {tr('boot')}: {state(snapshot.versions.boot)}
                  </div>
                  {detail(snapshot.versions.boot)}
                  {fold(
                    t('settings.system.diagnostics.packages', {
                      count: snapshot.versions.packages.length
                    }),
                    snapshot.versions.packages.map((pkg) => (
                      <div key={pkg.name}>
                        {line(
                          pkg.name,
                          pkg.version ||
                            t(`settings.system.diagnostics.states.${pkg.state}`, {
                              defaultValue: pkg.state
                            })
                        )}
                      </div>
                    ))
                  )}
                </>
              )}
              {card(
                tr('video'),
                <>
                  <div className="text-sm">
                    {tr('capture')}: {state({ state: snapshot.video.state })}
                  </div>
                  <div className="text-sm">
                    {tr('edid')}: {state({ state: snapshot.video.edidState })}
                  </div>
                  {line(tr('profile'), snapshot.video.profile)}
                  {line(tr('input'), snapshot.video.input)}
                  {line(tr('output'), snapshot.video.output)}
                  {line(tr('fps'), snapshot.video.measuredFps)}
                </>
              )}
              {card(
                tr('usb'),
                <>
                  <div className="text-sm">
                    {tr('binding')}: {state(snapshot.usb.binding, usbOff)}
                  </div>
                  {usbOff ? (
                    <p className="text-fg-muted mb-2 text-xs">{tr('usbOff')}</p>
                  ) : (
                    detail(snapshot.usb.binding)
                  )}
                  {line(tr('selected'), snapshot.usb.selected.join(', '))}
                </>
              )}
              {card(
                tr('firewall'),
                <>
                  <div className="text-sm">{state({ state: snapshot.firewall.state })}</div>
                  {detail({ state: snapshot.firewall.state, detail: snapshot.firewall.detail })}
                  {snapshot.firewall.hookChains.map((chain) => (
                    <div key={chain.label} className="border-line border-t py-1 text-xs">
                      {chain.feature
                        ? t(`settings.system.diagnostics.nftFeatures.${chain.feature}`, {
                            defaultValue: chain.feature
                          })
                        : chain.label}
                      : {chain.hook}
                      {chain.policy ? ` (${chain.policy})` : ''}
                    </div>
                  ))}
                </>
              )}
            </div>
            {card(
              tr('services'),
              <div className="space-y-3">
                {failing.map((service) => (
                  <div key={service.name}>
                    <div className="flex justify-between gap-3 text-sm">
                      <span>{service.name}</span>
                      {state(service)}
                    </div>
                    {detail(service)}
                  </div>
                ))}
                {running.length > 0 && (
                  <div className="text-sm">
                    <StatusBadge tone="success">{tr('states.running')}</StatusBadge>{' '}
                    <span className="text-fg-muted break-words">
                      {running.map((service) => service.name).join(', ')}
                    </span>
                  </div>
                )}
                {optionalIdle.length > 0 && (
                  <div className="text-fg-muted text-xs break-words">
                    {tr('optionalIdle')}:{' '}
                    {optionalIdle
                      .map(
                        (service) =>
                          `${service.name} (${t(`settings.system.diagnostics.states.${service.state}`, { defaultValue: service.state })})`
                      )
                      .join(', ')}
                  </div>
                )}
              </div>
            )}
            {card(
              tr('apk'),
              <>
                <div className="text-sm">
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
                  <p className="text-fg-muted mb-1 text-xs">{tr('operationFailed')}</p>
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
