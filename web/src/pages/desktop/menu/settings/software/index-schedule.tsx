import { useEffect, useState } from 'react';
import { Alert, message, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { TimeOfDay } from '@/components/time-of-day.tsx';
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
  // The switch and a chosen time apply at once.
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
            <Switch
              id="apk-index-refresh"
              aria-describedby="apk-index-refresh-description"
              checked={schedule.enabled}
              loading={busy}
              disabled={busy || !saved}
              onChange={(enabled) => void save({ ...schedule, enabled }, false)}
            />
          </SettingRow>
          {schedule.enabled && (
            <SettingRow
              label={t('time')}
              htmlFor="apk-index-refresh-time"
              description={timezone ? t('timezone', { timezone }) : undefined}
            >
              <TimeOfDay
                id="apk-index-refresh-time"
                hour={Number(schedule.time.slice(0, 2))}
                minute={Number(schedule.time.slice(3, 5))}
                disabled={busy || !saved}
                hourLabel={t('time')}
                minuteLabel={t('time')}
                onChange={(hour, minute) =>
                  void save(
                    {
                      ...schedule,
                      time: `${String(hour).padStart(2, '0')}:${String(minute).padStart(2, '0')}`
                    },
                    true
                  )
                }
              />
            </SettingRow>
          )}
        </>
      )}
    </Panel>
  );
};
