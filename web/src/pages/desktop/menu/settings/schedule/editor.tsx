import { useEffect, useState } from 'react';
import { Checkbox, Input, InputNumber, Modal, Radio, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/schedule.ts';
import { getScripts } from '@/api/script.ts';
import { formatDeviceMinute } from '@/lib/date-time.ts';
import { useDeviceTime } from '@/hooks/useDeviceTime.ts';
import { TimeOfDay } from '@/components/time-of-day.tsx';

import { fromCron, toCron, weekdayName, type When } from './when.ts';

type EditorProps = {
  task?: api.ScheduledTask;
  onClose: (saved: boolean) => void;
};

const defaults: Record<When['mode'], When> = {
  minutes: { mode: 'minutes', every: 15 },
  hourly: { mode: 'hourly', minute: 0 },
  daily: { mode: 'daily', hour: 3, minute: 0 },
  weekly: { mode: 'weekly', days: [1], hour: 3, minute: 0 },
  monthly: { mode: 'monthly', day: 1, hour: 3, minute: 0 },
  custom: { mode: 'custom', expression: '0 3 * * *' }
};

// Add or change a task: a name, what runs, and when, with the next runs the
// device computes for the schedule.
export const TaskEditor = ({ task, onClose }: EditorProps) => {
  const { t, i18n } = useTranslation();
  const [name, setName] = useState(task?.name ?? '');
  const [kind, setKind] = useState<api.ScheduledTask['kind']>(task?.kind ?? 'script');
  const [target, setTarget] = useState(task?.target ?? '');
  const [when, setWhen] = useState<When>(task ? fromCron(task.schedule) : defaults.daily);
  const [scripts, setScripts] = useState<string[]>([]);
  const [next, setNext] = useState<number[]>([]);
  // problem: the schedule as the device reads it; saveError: the last save.
  const [problem, setProblem] = useState('');
  const [saveError, setSaveError] = useState('');
  const [saving, setSaving] = useState(false);
  const schedule = toCron(when);
  const noDays = when.mode === 'weekly' && when.days.length === 0;

  useEffect(() => {
    void getScripts()
      .then((rsp) => setScripts(rsp.code === 0 ? (rsp.data?.files ?? []) : []))
      .catch(() => setScripts([]));
  }, []);

  // Any change makes a new attempt possible.
  useEffect(() => setSaveError(''), [name, kind, target, schedule]);

  useEffect(() => {
    if (noDays) return;
    let current = true;
    const timer = setTimeout(() => {
      api
        .previewSchedule(schedule)
        .then((rsp) => {
          if (!current) return;
          setNext(rsp.code === 0 ? rsp.data.next : []);
          setProblem(rsp.code === 0 ? '' : rsp.msg);
        })
        .catch(() => current && setProblem(t('settings.schedule.previewFailed')));
    }, 300);
    return () => {
      current = false;
      clearTimeout(timer);
    };
  }, [schedule, noDays, t]);

  const preferences = useDeviceTime();
  const ready = name.trim() !== '' && target.trim() !== '' && !problem && !noDays;

  async function save() {
    setSaving(true);
    try {
      const rsp = await api.saveTask({
        id: task?.id,
        name: name.trim(),
        enabled: task?.enabled ?? true,
        schedule,
        kind,
        target
      });
      if (rsp.code !== 0) {
        setSaveError(rsp.msg);
        return;
      }
      onClose(true);
    } catch {
      setSaveError(t('settings.schedule.saveFailed'));
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open
      title={task ? t('settings.schedule.editTitle') : t('settings.schedule.add')}
      okText={t('settings.schedule.save')}
      cancelText={t('settings.schedule.cancel')}
      okButtonProps={{ disabled: !ready }}
      confirmLoading={saving}
      onOk={() => void save()}
      onCancel={() => onClose(false)}
      destroyOnHidden
    >
      <div className="space-y-4">
        <div className="space-y-1">
          <div className="text-sm">{t('settings.schedule.name')}</div>
          <Input value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
        </div>

        <div className="space-y-2">
          <div className="text-sm">{t('settings.schedule.what')}</div>
          <Radio.Group
            value={kind}
            onChange={(e) => {
              setKind(e.target.value);
              setTarget('');
            }}
            options={[
              { value: 'script', label: t('settings.schedule.script') },
              { value: 'command', label: t('settings.schedule.command') }
            ]}
          />
          {kind === 'script' ? (
            <Select
              className="w-full"
              value={target || undefined}
              placeholder={
                scripts.length
                  ? t('settings.schedule.chooseScript')
                  : t('settings.schedule.noScripts')
              }
              options={scripts.map((file) => ({ value: file, label: file }))}
              onChange={setTarget}
            />
          ) : (
            <Input
              className="font-mono"
              value={target}
              maxLength={1024}
              placeholder="/usr/bin/curl -fsS https://example.com/ping"
              onChange={(e) => setTarget(e.target.value)}
            />
          )}
        </div>

        <div className="space-y-2">
          <div className="text-sm">{t('settings.schedule.when')}</div>
          <Select
            className="w-full"
            value={when.mode}
            onChange={(mode: When['mode']) =>
              setWhen(mode === 'custom' ? { mode, expression: schedule } : defaults[mode])
            }
            options={(['minutes', 'hourly', 'daily', 'weekly', 'monthly', 'custom'] as const).map(
              (mode) => ({ value: mode, label: t(`settings.schedule.mode.${mode}`) })
            )}
          />
          {when.mode === 'minutes' && (
            <InputNumber
              min={1}
              max={59}
              value={when.every}
              addonAfter={t('settings.schedule.minutesUnit')}
              onChange={(v) => v && setWhen({ ...when, every: v })}
            />
          )}
          {when.mode === 'hourly' && (
            <InputNumber
              min={0}
              max={59}
              value={when.minute}
              addonBefore={t('settings.schedule.minuteOfHour')}
              onChange={(v) => v !== null && setWhen({ ...when, minute: v })}
            />
          )}
          {when.mode === 'weekly' && (
            <Checkbox.Group
              value={when.days}
              onChange={(days) => setWhen({ ...when, days: days as number[] })}
              options={[1, 2, 3, 4, 5, 6, 0].map((d) => ({
                value: d,
                label: weekdayName(d, i18n.language)
              }))}
            />
          )}
          {when.mode === 'monthly' && (
            <InputNumber
              min={1}
              max={31}
              value={when.day}
              addonBefore={t('settings.schedule.dayOfMonth')}
              onChange={(v) => v && setWhen({ ...when, day: v })}
            />
          )}
          {'hour' in when && (
            <TimeOfDay
              hour={when.hour}
              minute={when.minute}
              hourLabel={t('settings.schedule.time')}
              minuteLabel={t('settings.schedule.minuteOfHour')}
              onChange={(hour, minute) => setWhen({ ...when, hour, minute })}
            />
          )}
          {when.mode === 'custom' && (
            <>
              <Input
                className="font-mono"
                value={when.expression}
                onChange={(e) => setWhen({ mode: 'custom', expression: e.target.value })}
              />
              <div className="text-fg-muted text-xs">{t('settings.schedule.expressionHelp')}</div>
            </>
          )}
        </div>

        <div className="space-y-1">
          <div className="text-sm">
            {t('settings.schedule.nextRuns')}{' '}
            <span className="text-fg-muted text-xs">({preferences.timezone})</span>
          </div>
          {noDays || problem ? (
            <div role="alert" className="text-danger text-xs">
              {noDays ? t('settings.schedule.noDays') : problem}
            </div>
          ) : (
            <ul className="text-fg-muted m-0 list-none p-0 font-mono text-xs">
              {next.map((at) => (
                <li key={at}>{formatDeviceMinute(at * 1000, preferences, i18n.language)}</li>
              ))}
              {next.length === 0 && <li>{t('settings.schedule.neverRuns')}</li>}
            </ul>
          )}
        </div>
        {saveError && (
          <div role="alert" className="text-danger text-xs">
            {saveError}
          </div>
        )}
      </div>
    </Modal>
  );
};
