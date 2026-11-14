import { useEffect, useState } from 'react';
import { Alert, Button, Checkbox, Select } from 'antd';
import { DownloadIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  agentInfo,
  groupSensors,
  isShown,
  newerVersion,
  sensorKey,
  sensorValue,
  viewOverrides,
  type HostSensor
} from '@/lib/host-sensors.ts';
import { http } from '@/lib/http';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

type Mode = 'off' | 'serial' | 'network';
type State = {
  installed: boolean;
  mode: Mode;
  serial: { port: string; state: string };
  network: { interface: string; addresses: string[]; path: string };
  agent: { path: string; available: boolean; size: number };
  linuxAgent?: { path: string; available: boolean; size: number };
  // The agent version the add-on brings; '' when unknown.
  agentVersion: string;
  status: {
    report: {
      host: string;
      os: string;
      agent: string;
      interval: number;
      limited?: boolean;
      sensors: HostSensor[];
    } | null;
    transport: string;
    age: number;
    stale: boolean;
  };
  // Which readings are shown: the exceptions to the default (lib/host-sensors).
  view: { overrides: Record<string, boolean> };
  // The helper disk with the agent, as the computer sees it.
  disk: { attached: boolean; stale: boolean; wanted: boolean };
};

const url = '/api/vm/host-sensors';

// Services > Computer sensors, the page of the add-on nanokvm-host-sensors:
// how the reports of the computer's nanokvm-sensors agent arrive, the agent
// itself, and the last report with the readings chosen to be shown.
export const HostSensorsPage = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.hostSensors' });
  const [state, setState] = useState<State>();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  // While choosing: every reading of the report by key, shown or not.
  const [draft, setDraft] = useState<Record<string, boolean>>();

  useEffect(() => {
    let disposed = false;
    const poll = async () => {
      try {
        const rsp = await http.request<State>({ method: 'get', url, timeout: 8000 });
        if (!disposed && rsp.code === 0) setState(rsp.data);
      } catch {
        // The last state stays; the age of the report shows it is old.
      }
    };
    void poll();
    const stop = pollWhileVisible(() => void poll(), 3000);
    return () => {
      disposed = true;
      stop();
    };
  }, []);

  const save = async (path: string, data: unknown) => {
    setSaving(true);
    setFailed(false);
    try {
      const rsp = await http.request<State>({ method: 'put', url: url + path, data });
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setState(rsp.data);
      return true;
    } catch {
      setFailed(true);
      return false;
    } finally {
      setSaving(false);
    }
  };

  const report = state?.status.report;
  const agent = report ? agentInfo(report.agent) : undefined;
  const overrides = state?.view?.overrides ?? {};
  const shownNow = (sensor: HostSensor) =>
    draft ? (draft[sensorKey(sensor)] ?? false) : isShown(sensor, overrides);
  // While choosing, every reading is listed; otherwise the chosen ones.
  const groups = groupSensors(report?.sensors.filter((s) => draft || shownNow(s)) ?? []);
  const startChoosing = () =>
    setDraft(
      Object.fromEntries((report?.sensors ?? []).map((s) => [sensorKey(s), isShown(s, overrides)]))
    );
  const saveChoice = async () => {
    if (!draft || !report) return;
    if (await save('/view', { overrides: viewOverrides(report.sensors, draft, overrides) }))
      setDraft(undefined);
  };
  const hint = !state
    ? ''
    : state.mode === 'off'
      ? t('offHint')
      : state.mode === 'serial' && state.serial.state === 'unavailable'
        ? t('serialUnavailable')
        : state.mode === 'serial' && state.serial.state === 'busy'
          ? t('serialBusy')
          : state.mode === 'serial' && state.serial.state === 'console'
            ? t('serialConsole')
            : state.mode === 'network' && state.network.addresses.length === 0
              ? t('networkUnavailable')
              : t('waiting');

  return (
    <div className="space-y-6">
      <p className="text-fg-muted m-0 text-sm">{t('about')}</p>
      {failed && <Alert type="error" showIcon message={t('saveFailed')} />}
      <SettingsSection title={t('connection')}>
        <SettingRow
          label={t('modeLabel')}
          htmlFor="host-sensors-mode"
          description={
            state?.mode === 'serial' ? t('serialNote', { port: state.serial.port }) : undefined
          }
        >
          <Select<Mode>
            id="host-sensors-mode"
            className="min-w-40"
            aria-describedby="host-sensors-mode-description"
            value={state?.mode}
            loading={saving || !state}
            disabled={saving || !state}
            onChange={(mode) => void save('', { mode })}
            options={(['off', 'serial', 'network'] as const).map((mode) => ({
              value: mode,
              label: t(`mode.${mode}`)
            }))}
          />
        </SettingRow>
        <SettingRow label={t('agent')} description={t('agentHint')} stacked>
          {state && (
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
              <StatusBadge
                tone={state.disk.attached ? (state.disk.stale ? 'warning' : 'success') : 'neutral'}
              >
                {t(
                  state.disk.attached
                    ? state.disk.stale
                      ? 'diskStale'
                      : 'diskAttached'
                    : 'diskMissing'
                )}
              </StatusBadge>
              {state.agent.available && !(state.disk.attached && !state.disk.stale) && (
                <a
                  href={state.agent.path}
                  download
                  className="text-info inline-flex items-center gap-1.5 text-sm"
                >
                  <DownloadIcon size={16} aria-hidden />
                  {t('download')} (Windows)
                </a>
              )}
              {state.linuxAgent?.available && !(state.disk.attached && !state.disk.stale) && (
                <a
                  href={state.linuxAgent.path}
                  download
                  className="text-info inline-flex items-center gap-1.5 text-sm"
                >
                  <DownloadIcon size={16} aria-hidden />
                  {t('download')} (Linux)
                </a>
              )}
            </div>
          )}
        </SettingRow>
      </SettingsSection>
      <SettingsSection
        title={t('readings')}
        actions={
          report &&
          (draft ? (
            <>
              <Button size="small" onClick={() => setDraft(undefined)} disabled={saving}>
                {t('cancel')}
              </Button>
              <Button
                size="small"
                type="primary"
                loading={saving}
                onClick={() => void saveChoice()}
              >
                {t('save')}
              </Button>
            </>
          ) : (
            <Button size="small" onClick={startChoosing}>
              {t('choose')}
            </Button>
          ))
        }
      >
        {!report && <p className="text-fg-muted m-0 text-sm">{hint}</p>}
        {report && state && (
          <Panel className="min-w-0">
            <div className="text-fg-muted mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
              <span className="text-fg wrap-break-word">
                {[report.host, report.os].filter(Boolean).join(' · ')}
              </span>
              <span>{t(`mode.${state.status.transport}`)}</span>
              {agent && (
                <span>
                  {t('agentVersion', { version: agent.version })}
                  {agent.system ? ` (${agent.system})` : ''}
                </span>
              )}
              <StatusBadge tone={state.status.stale ? 'danger' : 'success'}>
                {state.status.stale
                  ? t('stale')
                  : t('age', { seconds: Math.round(state.status.age / 1000) })}
              </StatusBadge>
            </div>
            {agent && state.agentVersion && newerVersion(state.agentVersion, agent.version) && (
              <Alert
                className="mb-3"
                type="warning"
                showIcon
                message={t('agentUpdate', { version: state.agentVersion })}
              />
            )}
            {report.limited && (
              <Alert className="mb-3" type="info" showIcon message={t('limited')} />
            )}
            {groups.length === 0 && <p className="text-fg-muted m-0 text-sm">{t('noneShown')}</p>}
            <div className="grid gap-x-6 gap-y-4 md:grid-cols-2">
              {groups.map((group) => {
                const on = group.sensors.filter(shownNow).length;
                return (
                  <div key={`${group.type}/${group.hardware}`} className="min-w-0 text-sm">
                    <div className="mb-1 flex items-center gap-2 font-medium wrap-break-word">
                      {draft && (
                        <Checkbox
                          aria-label={group.hardware}
                          checked={on === group.sensors.length}
                          indeterminate={on > 0 && on < group.sensors.length}
                          onChange={(event) =>
                            setDraft({
                              ...draft,
                              ...Object.fromEntries(
                                group.sensors.map((s) => [sensorKey(s), event.target.checked])
                              )
                            })
                          }
                        />
                      )}
                      <span className="min-w-0">{group.hardware}</span>
                    </div>
                    <dl className="m-0">
                      {group.sensors.map((sensor, index) => (
                        <div
                          key={`${sensor.kind}/${sensor.name}/${index}`}
                          className="flex justify-between gap-3 py-0.5"
                        >
                          <dt className="text-fg-muted flex min-w-0 items-start gap-2 wrap-break-word">
                            {draft && (
                              <Checkbox
                                aria-label={sensor.name}
                                checked={shownNow(sensor)}
                                onChange={(event) =>
                                  setDraft({ ...draft, [sensorKey(sensor)]: event.target.checked })
                                }
                              />
                            )}
                            <span className="min-w-0">{sensor.name}</span>
                          </dt>
                          <dd className="text-fg m-0 shrink-0 tabular-nums">
                            {sensorValue(sensor)}
                          </dd>
                        </div>
                      ))}
                    </dl>
                  </div>
                );
              })}
            </div>
          </Panel>
        )}
      </SettingsSection>
    </div>
  );
};
