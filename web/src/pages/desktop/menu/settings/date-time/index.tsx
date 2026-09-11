import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Divider, Select, Spin, Tag } from 'antd';
import { useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { formatDeviceTime, type DateTimeConfig } from '@/lib/date-time.ts';
import { http } from '@/lib/http.ts';
import { timePreferencesAtom } from '@/hooks/useDeviceTime.ts';

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
    const poll = window.setInterval(() => void refresh(), 30000);
    const tick = window.setInterval(() => setElapsed(performance.now() - received.current), 1000);
    return () => {
      mounted.current = false;
      window.clearInterval(poll);
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
      if (rsp.code !== 0) throw new Error(rsp.msg);
      if (!mounted.current) return;
      dirty.current = false;
      received.current = performance.now();
      setElapsed(0);
      setStatus(rsp.data);
      setConfig(rsp.data.config);
      setPreferences(rsp.data.config);
      setSaved(true);
    } catch {
      if (mounted.current) setError(t('dateTime.saveFailed'));
    } finally {
      operation.current = false;
      if (mounted.current) setBusy(false);
    }
  }

  return (
    <div className="flex flex-col gap-5">
      <div className="text-base">{t('dateTime.title')}</div>
      <Divider className="!my-0 opacity-50" />
      {error && <Alert type="error" showIcon message={error} />}
      {!status || !config ? (
        <Spin />
      ) : (
        <>
          <div className="rounded-lg bg-neutral-800/60 p-4">
            <div className="mb-2 text-xs text-neutral-400">{t('dateTime.deviceTime')}</div>
            <div className="text-xl tabular-nums">
              {formatDeviceTime(status.now + elapsed, status.config, i18n.language, true)}
            </div>
            <div className="mt-2 flex flex-wrap items-center gap-2 text-xs text-neutral-400">
              <span>{status.config.timezone}</span>
              <Tag bordered={false} color={status.synchronized ? 'green' : 'default'}>
                {t(status.synchronized ? 'dateTime.synchronized' : 'dateTime.waiting')}
              </Tag>
            </div>
          </div>
          <label className="flex flex-col gap-2 text-sm">
            <span>{t('dateTime.timezone')}</span>
            <Select
              aria-label={t('dateTime.timezone')}
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
          </label>
          <label className="flex flex-col gap-2 text-sm">
            <span>{t('dateTime.format')}</span>
            <Select
              aria-label={t('dateTime.format')}
              value={config.format}
              disabled={busy}
              options={[
                { value: '24', label: t('dateTime.hour24') },
                { value: '12', label: t('dateTime.hour12') }
              ]}
              onChange={(format) => change({ format })}
            />
            <span className="text-xs text-neutral-400">
              {t('dateTime.preview')}:{' '}
              {formatDeviceTime(status.now + elapsed, config, i18n.language)}
            </span>
          </label>
          <label className="flex flex-col gap-2 text-sm">
            <span>{t('dateTime.servers')}</span>
            <Select
              aria-label={t('dateTime.servers')}
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
            <span className="text-xs text-neutral-400">{t('dateTime.serversHelp')}</span>
          </label>
          <p className="text-xs text-neutral-400">{t('dateTime.scope')}</p>
          <Button
            type="primary"
            loading={busy}
            disabled={!dirty.current || config.servers.length === 0}
            onClick={() => void save()}
          >
            {t('dateTime.save')}
          </Button>
          {saved && <Alert type="success" showIcon message={t('dateTime.saved')} />}
        </>
      )}
    </div>
  );
}
