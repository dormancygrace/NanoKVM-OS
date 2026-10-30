import { useEffect, useState } from 'react';
import { Alert, Input, message, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

type Schedule = { enabled: boolean; time: string };

export const IndexSchedule = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.software.indexSchedule' });
  const [saved, setSaved] = useState<Schedule>();
  const [schedule, setSchedule] = useState<Schedule>({ enabled: false, time: '03:00' });
  const [timezone, setTimezone] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    http
      .get<{ schedule: Schedule; timezone: string }>('/api/os/update/software/index-schedule')
      .then((rsp) => {
        if (!active) return;
        if (rsp.code !== 0) throw new Error(rsp.msg);
        setSaved(rsp.data.schedule);
        setSchedule(rsp.data.schedule);
        setTimezone(rsp.data.timezone);
      })
      .catch(() => {
        if (active) setError(t('failed'));
      });
    return () => {
      active = false;
    };
  }, [t]);

  const validTime = (time: string) => /^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(time);
  // The switch applies at once; a new time applies when the field is left.
  const save = async (next: Schedule, announce: boolean) => {
    if (busy || !saved || !validTime(next.time)) return;
    if (next.enabled === saved.enabled && next.time === saved.time) return;
    setBusy(true);
    try {
      const rsp = await http.request({
        method: 'put',
        url: '/api/os/update/software/index-schedule',
        data: next
      });
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setSaved(next);
      setSchedule(next);
      setError('');
      if (announce) void message.success(t('saved'));
    } catch (err) {
      setSchedule(saved);
      setError(err instanceof Error ? err.message : t('failed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel className="space-y-3">
      {error && <Alert type="error" title={error} showIcon />}
      {!saved && !error ? (
        <Spin size="small" />
      ) : (
        <>
          <SettingRow label={t('enabled')} description={t('hint')} htmlFor="apk-index-refresh">
            <div onBlur={() => void save(schedule, true)}>
              <Switch
                id="apk-index-refresh"
                aria-describedby="apk-index-refresh-description"
                checked={schedule.enabled}
                loading={busy}
                disabled={busy || !saved}
                // A half-typed time does not block the switch: the saved one stays.
                onChange={(enabled) =>
                  void save(
                    {
                      ...schedule,
                      enabled,
                      time: validTime(schedule.time) || !saved ? schedule.time : saved.time
                    },
                    false
                  )
                }
              />
            </div>
          </SettingRow>
          {schedule.enabled && (
            <SettingRow
              label={t('time')}
              htmlFor="apk-index-refresh-time"
              description={timezone ? t('timezone', { timezone }) : undefined}
            >
              <Input
                id="apk-index-refresh-time"
                type="time"
                step={60}
                value={schedule.time}
                className="w-36"
                status={validTime(schedule.time) ? undefined : 'error'}
                disabled={busy || !saved}
                onChange={(event) => setSchedule({ ...schedule, time: event.target.value })}
                onBlur={(event) => {
                  // A click on the switch saves both fields. Saving here would
                  // disable it between pointer-down and click, losing the toggle.
                  // If focus only passes through the switch, its blur saves time.
                  if (event.relatedTarget?.id !== 'apk-index-refresh') void save(schedule, true);
                }}
                onPressEnter={() => void save(schedule, true)}
              />
            </SettingRow>
          )}
        </>
      )}
    </Panel>
  );
};
