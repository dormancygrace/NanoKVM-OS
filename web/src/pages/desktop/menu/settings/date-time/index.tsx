import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, message, Select, Spin } from 'antd';
import { useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { formatDeviceTime, type DateFormat, type DateTimeConfig } from '@/lib/date-time.ts';
import { http } from '@/lib/http.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { timePreferencesAtom } from '@/hooks/useDeviceTime.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

type Status = { config: DateTimeConfig; zones: string[]; now: number; synchronized: boolean };

// What is being applied: one select, the server list or the clock.
type Operation = 'timezone' | 'format' | 'dateFormat' | 'servers' | 'clock';

// System > Date & time. The device (time zone, NTP servers) and the display
// formats of the interface. The selects apply at once; the server list,
// which restarts synchronisation, has its own Save.
export function DateTimeSettings() {
  const { t, i18n } = useTranslation();
  const setPreferences = useSetAtom(timePreferencesAtom);
  const [status, setStatus] = useState<Status>();
  const [servers, setServers] = useState<string[]>([]);
  const [busy, setBusy] = useState<Operation>();
  const [loadError, setLoadError] = useState('');
  const [elapsed, setElapsed] = useState(0);
  const received = useRef(0);
  const mounted = useRef(false);
  const operation = useRef(false);
  const serversEdited = useRef(false);

  const show = useCallback(
    (next: Status) => {
      received.current = performance.now();
      setElapsed(0);
      setStatus(next);
      setPreferences(next.config);
      if (!serversEdited.current) setServers(next.config.servers);
    },
    [setPreferences]
  );

  useEffect(() => {
    mounted.current = true;
    const refresh = async () => {
      if (operation.current) return;
      try {
        const rsp = await http.get<Status>('/api/vm/date-time');
        if (rsp.code !== 0) throw new Error(rsp.msg);
        if (!mounted.current || operation.current) return;
        show(rsp.data);
        setLoadError('');
      } catch {
        if (mounted.current) setLoadError(t('dateTime.loadFailed'));
      }
    };
    void refresh();
    const stopPolling = pollWhileVisible(() => void refresh(), 30000);
    const tick = window.setInterval(() => setElapsed(performance.now() - received.current), 1000);
    return () => {
      mounted.current = false;
      stopPolling();
      window.clearInterval(tick);
    };
  }, [show, t]);

  async function run(kind: Operation, request: () => Promise<{ code: number; data: Status }>) {
    if (operation.current) return false;
    operation.current = true;
    setBusy(kind);
    try {
      const rsp = await request();
      if (!mounted.current) return false;
      if (rsp.code !== 0) {
        showRequestError(rsp, kind === 'clock' ? 'dateTime.clockFailed' : 'dateTime.saveFailed');
        return false;
      }
      if (kind === 'servers') serversEdited.current = false;
      show(rsp.data);
      return true;
    } catch (err) {
      if (mounted.current)
        showRequestError(err, kind === 'clock' ? 'dateTime.clockFailed' : 'dateTime.saveFailed');
      return false;
    } finally {
      operation.current = false;
      if (mounted.current) setBusy(undefined);
    }
  }

  // One setting with the stored others: servers being edited are not sent.
  function apply(kind: Exclude<Operation, 'clock'>, patch: Partial<DateTimeConfig>) {
    if (!status) return Promise.resolve(false);
    return run(kind, () => http.post<Status>('/api/vm/date-time', { ...status.config, ...patch }));
  }

  async function saveServers() {
    if (await apply('servers', { servers })) void message.success(t('dateTime.saved'));
  }

  async function setClockFromBrowser() {
    if (await run('clock', () => http.post<Status>('/api/vm/date-time/clock', { now: Date.now() })))
      void message.success(t('dateTime.clockSet'));
  }

  if (!status) return loadError ? <Alert type="error" showIcon message={loadError} /> : <Spin />;

  return (
    <div className="space-y-6">
      {loadError && <Alert type="error" showIcon message={loadError} />}
      <Panel>
        <div className="text-fg-muted mb-2 text-xs">{t('dateTime.deviceTime')}</div>
        <div className="text-xl tabular-nums">
          {formatDeviceTime(status.now + elapsed, status.config, i18n.language, true)}
        </div>
        <div className="text-fg-muted mt-2 flex flex-wrap items-center gap-2 text-xs">
          <span>{status.config.timezone}</span>
          {/* Not yet, or no network: a state, not an error. */}
          <StatusBadge tone={status.synchronized ? 'success' : 'warning'}>
            {t(status.synchronized ? 'dateTime.synchronized' : 'dateTime.waiting')}
          </StatusBadge>
        </div>
        {!status.synchronized && (
          <div className="mt-3 space-y-1">
            <Button
              size="small"
              loading={busy === 'clock'}
              disabled={!!busy}
              onClick={() => void setClockFromBrowser()}
            >
              {t('dateTime.setFromBrowser')}
            </Button>
            <div className="text-fg-muted text-xs">{t('dateTime.setFromBrowserHint')}</div>
          </div>
        )}
      </Panel>
      <SettingsSection title={t('dateTime.device')}>
        <SettingRow label={t('dateTime.timezone')} htmlFor="date-time-timezone">
          <Select
            id="date-time-timezone"
            aria-label={t('dateTime.timezone')}
            style={{ width: 260, maxWidth: '100%' }}
            popupMatchSelectWidth={false}
            showSearch
            optionFilterProp="label"
            value={status.config.timezone}
            loading={busy === 'timezone'}
            disabled={!!busy}
            options={status.zones.map((zone) => ({
              value: zone,
              label: zone.replace(/_/g, ' ')
            }))}
            onChange={(timezone) => void apply('timezone', { timezone })}
          />
        </SettingRow>
        <SettingRow
          label={t('dateTime.servers')}
          description={t('dateTime.serversHelp')}
          htmlFor="date-time-servers"
          stacked
        >
          <Select
            id="date-time-servers"
            aria-label={t('dateTime.servers')}
            aria-describedby="date-time-servers-description"
            className="w-full"
            mode="tags"
            value={servers}
            disabled={!!busy}
            tokenSeparators={[',', ' ']}
            maxCount={6}
            options={[
              '0.pool.ntp.org',
              '1.pool.ntp.org',
              '2.pool.ntp.org',
              '3.pool.ntp.org',
              'time.cloudflare.com',
              'time.google.com'
            ].map((value) => ({ value }))}
            onChange={(next) => {
              serversEdited.current = true;
              setServers(next);
            }}
          />
        </SettingRow>
        <div>
          <Button
            type="primary"
            loading={busy === 'servers'}
            disabled={!serversEdited.current || servers.length === 0 || !!busy}
            onClick={() => void saveServers()}
          >
            {t('dateTime.save')}
          </Button>
        </div>
      </SettingsSection>
      <SettingsSection title={t('dateTime.display')} description={t('dateTime.displayHint')}>
        <SettingRow label={t('dateTime.format')} htmlFor="date-time-format">
          <Select
            id="date-time-format"
            aria-label={t('dateTime.format')}
            style={{ width: 180 }}
            value={status.config.format}
            loading={busy === 'format'}
            disabled={!!busy}
            options={[
              { value: '24', label: t('dateTime.hour24') },
              { value: '12', label: t('dateTime.hour12') }
            ]}
            onChange={(format) => void apply('format', { format })}
          />
        </SettingRow>
        <SettingRow label={t('dateTime.dateFormat')} htmlFor="date-time-date-format">
          <Select
            id="date-time-date-format"
            aria-label={t('dateTime.dateFormat')}
            style={{ width: 180 }}
            value={status.config.dateFormat ?? 'auto'}
            loading={busy === 'dateFormat'}
            disabled={!!busy}
            options={[
              { value: 'auto', label: t('dateTime.dateAuto') },
              { value: 'dmy', label: t('dateTime.dateDmy') },
              { value: 'mdy', label: t('dateTime.dateMdy') },
              { value: 'ymd', label: t('dateTime.dateYmd') }
            ]}
            onChange={(dateFormat: DateFormat) => void apply('dateFormat', { dateFormat })}
          />
        </SettingRow>
      </SettingsSection>
    </div>
  );
}
