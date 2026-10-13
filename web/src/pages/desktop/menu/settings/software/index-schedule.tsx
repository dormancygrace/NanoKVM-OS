import { useEffect, useState } from 'react';
import { Alert, Button, Checkbox, Input, message, Spin } from 'antd';
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
      .get('/api/os/update/software/index-schedule')
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

  const validTime = /^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(schedule.time);
  const dirty = saved?.enabled !== schedule.enabled || saved?.time !== schedule.time;
  const save = async () => {
    if (busy || !saved || !validTime) return;
    setBusy(true);
    try {
      const rsp = await http.request({
        method: 'put',
        url: '/api/os/update/software/index-schedule',
        data: schedule
      });
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setSaved(schedule);
      setError('');
      void message.success(t('saved'));
    } catch (err) {
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
          <Checkbox
            checked={schedule.enabled}
            disabled={busy || !saved}
            onChange={(event) => setSchedule({ ...schedule, enabled: event.target.checked })}
          >
            {t('enabled')}
          </Checkbox>
          <p className="text-fg-muted m-0 text-xs">{t('hint')}</p>
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
              disabled={busy || !saved || !schedule.enabled}
              onChange={(event) => setSchedule({ ...schedule, time: event.target.value })}
            />
          </SettingRow>
          <Button
            type="primary"
            loading={busy}
            disabled={!saved || !dirty || !validTime}
            onClick={save}
          >
            {t('save')}
          </Button>
        </>
      )}
    </Panel>
  );
};
