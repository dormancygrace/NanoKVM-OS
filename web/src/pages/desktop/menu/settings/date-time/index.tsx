import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Select, Spin } from 'antd';
import { useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { formatDeviceTime, type DateTimeConfig } from '@/lib/date-time.ts';
import { http } from '@/lib/http.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { timePreferencesAtom } from '@/hooks/useDeviceTime.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

type Status = { config: DateTimeConfig; zones: string[]; now: number; synchronized: boolean };

export function DateTimeSettings() {
  const { t, i18n } = useTranslation();
  const setPreferences = useSetAtom(timePreferencesAtom);
  const [status, setStatus] = useState<Status>();
  const [config, setConfig] = useState<DateTimeConfig>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [elapsed, setElapsed] = useState(0);
  const received = useRef(0);
  const mounted = useRef(false);
  const operation = useRef(false);
  const dirty = useRef(false);

  useEffect(() => {
    mounted.current = true;
    const refresh = async () => {
      if (operation.current) return;
      try {
        const rsp = await http.get('/api/vm/date-time');
        if (rsp.code !== 0) throw new Error(rsp.msg);
        if (!mounted.current || operation.current) return;
        received.current = performance.now();
        setElapsed(0);
        setStatus(rsp.data);
        setPreferences(rsp.data.config);
        if (!dirty.current) setConfig(rsp.data.config);
        setError('');
      } catch {
        if (mounted.current) setError(t('dateTime.loadFailed'));
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
  }, [setPreferences, t]);

  function change(patch: Partial<DateTimeConfig>) {
    dirty.current = true;
    setSaved(false);
    setConfig((current) => (current ? { ...current, ...patch } : current));
  }

  async function save() {
    if (!config || operation.current) return;
    operation.current = true;
    setBusy(true);
    setError('');
    setSaved(false);
    try {
      const rsp = await http.post('/api/vm/date-time', config);
      if (!mounted.current) return;
      if (rsp.code !== 0) {
        showRequestError(rsp, 'dateTime.saveFailed');
        return;
      }
      dirty.current = false;
      received.current = performance.now();
      setElapsed(0);
      setStatus(rsp.data);
      setConfig(rsp.data.config);
      setPreferences(rsp.data.config);
      setSaved(true);
    } catch (err) {
      if (mounted.current) showRequestError(err, 'dateTime.saveFailed');
    } finally {
      operation.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      {error && <Alert type="error" showIcon message={error} />}
      {!status || !config ? (
        <Spin />
      ) : (
        <>
          <Panel>
            <div className="text-fg-muted mb-2 text-xs">{t('dateTime.deviceTime')}</div>
            <div className="text-xl tabular-nums">
              {formatDeviceTime(status.now + elapsed, status.config, i18n.language, true)}
            </div>
            <div className="text-fg-muted mt-2 flex flex-wrap items-center gap-2 text-xs">
              <span>{status.config.timezone}</span>
              <StatusBadge tone={status.synchronized ? 'success' : 'neutral'}>
                {t(status.synchronized ? 'dateTime.synchronized' : 'dateTime.waiting')}
              </StatusBadge>
            </div>
          </Panel>
          <SettingsSection>
            <SettingRow label={t('dateTime.timezone')} htmlFor="date-time-timezone">
              <Select
                id="date-time-timezone"
                aria-label={t('dateTime.timezone')}
                style={{ width: 180 }}
                popupMatchSelectWidth={false}
                showSearch
                optionFilterProp="label"
                value={config.timezone}
                disabled={busy}
                options={status.zones.map((zone) => ({
                  value: zone,
                  label: zone.replace(/_/g, ' ')
                }))}
                onChange={(timezone) => change({ timezone })}
              />
            </SettingRow>
            <SettingRow
              label={t('dateTime.format')}
              description={
                <>
                  {t('dateTime.preview')}:{' '}
                  {formatDeviceTime(status.now + elapsed, config, i18n.language)}
                </>
              }
              htmlFor="date-time-format"
            >
              <Select
                id="date-time-format"
                aria-label={t('dateTime.format')}
                aria-describedby="date-time-format-description"
                style={{ width: 180 }}
                value={config.format}
                disabled={busy}
                options={[
                  { value: '24', label: t('dateTime.hour24') },
                  { value: '12', label: t('dateTime.hour12') }
                ]}
                onChange={(format) => change({ format })}
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
                value={config.servers}
                disabled={busy}
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
                onChange={(servers) => change({ servers })}
              />
            </SettingRow>
            <p className="text-fg-muted mb-0 text-xs">{t('dateTime.scope')}</p>
            <div>
              <Button
                type="primary"
                loading={busy}
                disabled={!dirty.current || config.servers.length === 0}
                onClick={() => void save()}
              >
                {t('dateTime.save')}
              </Button>
            </div>
            {saved && <Alert type="success" showIcon message={t('dateTime.saved')} />}
          </SettingsSection>
        </>
      )}
    </div>
  );
}
