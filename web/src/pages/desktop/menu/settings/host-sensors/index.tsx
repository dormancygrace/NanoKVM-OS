import { useEffect, useState } from 'react';
import { Alert, Select } from 'antd';
import { DownloadIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { groupSensors, mainKinds, sensorValue, type HostSensor } from '@/lib/host-sensors.ts';
import { http } from '@/lib/http';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

type Mode = 'off' | 'serial' | 'network';
type State = {
  installed: boolean;
  mode: Mode;
  serial: { port: string; state: string };
  network: { interface: string; addresses: string[]; path: string };
  agent: { path: string; available: boolean; sha256: string; size: number };
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
};

const url = '/api/vm/host-sensors';

// Services > Computer sensors, the page of the add-on nanokvm-host-sensors:
// how the reports of the computer's nanokvm-sensors agent arrive, the agent
// itself, and the last report.
export const HostSensorsPage = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.hostSensors' });
  const [state, setState] = useState<State>();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const [all, setAll] = useState(false);

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

  const setMode = async (mode: Mode) => {
    setSaving(true);
    setFailed(false);
    try {
      const rsp = await http.request<State>({ method: 'put', url, data: { mode } });
      if (rsp.code === 0) setState(rsp.data);
      else setFailed(true);
    } catch {
      setFailed(true);
    } finally {
      setSaving(false);
    }
  };

  const report = state?.status.report;
  const shown = report?.sensors.filter((s) => all || mainKinds.has(s.kind)) ?? [];
  const groups = groupSensors(shown);
  const hint = !state
    ? ''
    : state.mode === 'off'
      ? t('offHint')
      : state.mode === 'serial' && state.serial.state === 'unavailable'
        ? t('serialUnavailable')
        : state.mode === 'serial' && state.serial.state === 'busy'
          ? t('serialBusy')
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
            onChange={(mode) => void setMode(mode)}
            options={(['off', 'serial', 'network'] as const).map((mode) => ({
              value: mode,
              label: t(`mode.${mode}`)
            }))}
          />
        </SettingRow>
        <SettingRow label={t('agent')} description={t('agentHint')}>
          {state?.agent.available && (
            <a
              href={state.agent.path}
              download
              className="text-info inline-flex items-center gap-1.5 text-sm"
            >
              <DownloadIcon size={16} aria-hidden />
              {t('download')}
            </a>
          )}
        </SettingRow>
      </SettingsSection>
      <SettingsSection title={t('readings')}>
        {!report && <p className="text-fg-muted m-0 text-sm">{hint}</p>}
        {report && state && (
          <Panel className="min-w-0">
            <div className="text-fg-muted mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
              <span className="text-fg wrap-break-word">
                {[report.host, report.os].filter(Boolean).join(' · ')}
              </span>
              <span>{t(`mode.${state.status.transport}`)}</span>
              <StatusBadge tone={state.status.stale ? 'danger' : 'success'}>
                {state.status.stale
                  ? t('stale')
                  : t('age', { seconds: Math.round(state.status.age / 1000) })}
              </StatusBadge>
            </div>
            {report.limited && (
              <Alert className="mb-3" type="info" showIcon message={t('limited')} />
            )}
            <div className="grid gap-x-6 gap-y-4 md:grid-cols-2">
              {groups.map((group) => (
                <div key={`${group.type}/${group.hardware}`} className="min-w-0 text-sm">
                  <div className="mb-1 font-medium wrap-break-word">{group.hardware}</div>
                  <dl className="m-0">
                    {group.sensors.map((sensor, index) => (
                      <div
                        key={`${sensor.kind}/${sensor.name}/${index}`}
                        className="flex justify-between gap-3 py-0.5"
                      >
                        <dt className="text-fg-muted min-w-0 wrap-break-word">{sensor.name}</dt>
                        <dd className="text-fg m-0 shrink-0 tabular-nums">{sensorValue(sensor)}</dd>
                      </div>
                    ))}
                  </dl>
                </div>
              ))}
            </div>
            {report.sensors.length > 0 && (
              <button
                type="button"
                className="text-info mt-3 border-0! bg-transparent! !p-0 text-xs"
                onClick={() => setAll(!all)}
              >
                {all ? t('showMain') : t('showAll', { count: report.sensors.length })}
              </button>
            )}
          </Panel>
        )}
      </SettingsSection>
    </div>
  );
};
