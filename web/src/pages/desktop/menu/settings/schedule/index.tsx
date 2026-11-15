import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Popconfirm, Switch, Tooltip } from 'antd';
import { FileTextIcon, PencilIcon, PlayIcon, PlusIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/schedule.ts';
import { formatDeviceMinute } from '@/lib/date-time.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { useDeviceTime } from '@/hooks/useDeviceTime.ts';
import { Fold, Panel, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

import { TaskEditor } from './editor.tsx';
import { describe } from './when.ts';

// Services > Scheduled tasks: scripts of the Scripts window or commands run
// by cron. The other jobs of the crontab are shown and never changed.
export const ScheduledTasks = () => {
  const { t, i18n } = useTranslation();
  const [tasks, setTasks] = useState<api.TaskStatus[]>([]);
  const [other, setOther] = useState<string[]>([]);
  const [crond, setCrond] = useState(true);
  const preferences = useDeviceTime();
  const [loaded, setLoaded] = useState(false);
  const [editing, setEditing] = useState<api.ScheduledTask | 'new'>();
  const [output, setOutput] = useState<{ name: string; log: string }>();
  const [busy, setBusy] = useState('');
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    const rsp = await api.getTasks();
    if (!mounted.current) return;
    if (rsp.code !== 0) {
      showRequestError(rsp);
      return;
    }
    setTasks(rsp.data.tasks ?? []);
    setOther(rsp.data.other ?? []);
    setCrond(rsp.data.crond);
    setLoaded(true);
  }, []);

  useEffect(() => {
    mounted.current = true;
    const poll = () => void refresh().catch(() => undefined);
    poll();
    const stop = pollWhileVisible(poll, 5000);
    return () => {
      mounted.current = false;
      stop();
    };
  }, [refresh]);

  async function act(id: string, action: () => Promise<{ code: number; msg: string }>) {
    setBusy(id);
    try {
      const rsp = await action();
      if (rsp.code !== 0) showRequestError(rsp);
    } catch (err) {
      showRequestError(err);
    } finally {
      setBusy('');
      await refresh().catch(() => undefined);
    }
  }

  async function showOutput(task: api.TaskStatus) {
    try {
      const rsp = await api.getTaskLog(task.id);
      if (rsp.code !== 0) {
        showRequestError(rsp);
        return;
      }
      setOutput({ name: task.name, log: rsp.data.log });
    } catch (err) {
      showRequestError(err);
    }
  }

  const at = (seconds: number) => formatDeviceMinute(seconds * 1000, preferences, i18n.language);

  function lastRun(task: api.TaskStatus) {
    if (task.state === 'running' && task.started)
      return (
        <StatusBadge tone="warning">
          {t('settings.schedule.running', { time: at(task.started) })}
        </StatusBadge>
      );
    const code = task.state?.startsWith('exited ') ? Number(task.state.slice(7)) : undefined;
    if (code !== undefined && task.ended)
      return (
        <StatusBadge tone={code === 0 ? 'success' : 'danger'}>
          {t('settings.schedule.exited', { time: at(task.ended), code })}
        </StatusBadge>
      );
    return <span className="text-fg-muted text-xs">{t('settings.schedule.notRun')}</span>;
  }

  return (
    <div className="space-y-6">
      <SettingsSection>
        <p className="text-fg-muted mt-0 text-sm">
          {t('settings.schedule.description', { zone: preferences.timezone })}
        </p>
        {loaded && !crond && (
          <Alert type="warning" showIcon message={t('settings.schedule.crondStopped')} />
        )}
        <div>
          <Button icon={<PlusIcon size={16} />} onClick={() => setEditing('new')}>
            {t('settings.schedule.add')}
          </Button>
        </div>
      </SettingsSection>

      <SettingsSection>
        {loaded && tasks.length === 0 && (
          <Panel flush className="text-fg-muted border-dashed p-6 text-center text-sm">
            {t('settings.schedule.empty')}
          </Panel>
        )}
        {tasks.length > 0 && (
          <Panel flush>
            <ul className="divide-line m-0 list-none divide-y p-0">
              {tasks.map((task) => (
                <li key={task.id} className="space-y-2 px-4 py-3">
                  <div className="flex items-center justify-between gap-3">
                    <div className="min-w-0">
                      <div className="truncate font-medium" title={task.name}>
                        {task.name}
                      </div>
                      <div className="text-fg-muted text-xs">
                        {describe(task.schedule, t, i18n.language, preferences)}
                      </div>
                    </div>
                    <div className="flex shrink-0 items-center gap-1">
                      <Tooltip title={t('settings.schedule.runNow')}>
                        <Button
                          type="text"
                          size="small"
                          icon={<PlayIcon size={14} />}
                          aria-label={`${t('settings.schedule.runNow')} ${task.name}`}
                          disabled={busy !== ''}
                          onClick={() => void act(task.id, () => api.runTask(task.id))}
                        />
                      </Tooltip>
                      <Tooltip title={t('settings.schedule.output')}>
                        <Button
                          type="text"
                          size="small"
                          icon={<FileTextIcon size={14} />}
                          aria-label={`${t('settings.schedule.output')} ${task.name}`}
                          onClick={() => void showOutput(task)}
                        />
                      </Tooltip>
                      <Tooltip title={t('settings.schedule.edit')}>
                        <Button
                          type="text"
                          size="small"
                          icon={<PencilIcon size={14} />}
                          aria-label={`${t('settings.schedule.edit')} ${task.name}`}
                          disabled={busy !== ''}
                          onClick={() => setEditing(task)}
                        />
                      </Tooltip>
                      <Popconfirm
                        title={t('settings.schedule.deleteConfirm', { name: task.name })}
                        okText={t('settings.schedule.delete')}
                        cancelText={t('settings.schedule.cancel')}
                        onConfirm={() => void act(task.id, () => api.deleteTask(task.id))}
                      >
                        <Button
                          type="text"
                          size="small"
                          danger
                          icon={<Trash2Icon size={14} />}
                          aria-label={`${t('settings.schedule.delete')} ${task.name}`}
                          disabled={busy !== ''}
                        />
                      </Popconfirm>
                      <Switch
                        size="small"
                        checked={task.enabled}
                        loading={busy === task.id}
                        disabled={busy !== ''}
                        aria-label={`${t('settings.schedule.enabled')} ${task.name}`}
                        onChange={(enabled) =>
                          void act(task.id, () => api.saveTask({ ...task, enabled }))
                        }
                      />
                    </div>
                  </div>
                  <div className="text-fg-muted truncate font-mono text-xs" title={task.target}>
                    {task.kind === 'script' ? `${t('settings.schedule.script')}: ` : '$ '}
                    {task.target}
                  </div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                    {lastRun(task)}
                    {task.enabled && task.next?.[0] && (
                      <span className="text-fg-muted">
                        {t('settings.schedule.nextRun', { time: at(task.next[0]) })}
                      </span>
                    )}
                  </div>
                </li>
              ))}
            </ul>
          </Panel>
        )}
        {other.length > 0 && (
          <Fold title={t('settings.schedule.other')}>
            <p className="text-fg-muted mt-0 text-xs">{t('settings.schedule.otherHelp')}</p>
            <ul className="m-0 list-none space-y-1 p-0 font-mono text-xs break-all">
              {other.map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
          </Fold>
        )}
      </SettingsSection>

      {editing && (
        <TaskEditor
          task={editing === 'new' ? undefined : editing}
          onClose={(saved) => {
            setEditing(undefined);
            if (saved) void refresh().catch(() => undefined);
          }}
        />
      )}
      <Modal
        open={!!output}
        title={output?.name}
        footer={null}
        width={640}
        onCancel={() => setOutput(undefined)}
      >
        <pre className="bg-surface-raised m-0 max-h-[60vh] overflow-auto rounded p-3 text-xs whitespace-pre-wrap">
          {output?.log || t('settings.schedule.noOutput')}
        </pre>
      </Modal>
    </div>
  );
};
