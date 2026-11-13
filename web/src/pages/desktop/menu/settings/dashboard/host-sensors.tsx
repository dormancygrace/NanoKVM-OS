import { useEffect, useState } from 'react';
import { Alert, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import { groupSensors, mainKinds, sensorValue, type HostSensor } from '@/lib/host-sensors.ts';
import { http } from '@/lib/http';
import { Panel, StatusBadge } from '@/components/ui/settings.tsx';

type Mode = 'off' | 'serial' | 'network';
type State = {
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

// The managed computer's sensors, as its nanokvm-sensors agent reports them:
// how they arrive (an administrator's choice) and the last report.
export const HostSensors = ({ admin }: { admin: boolean }) => {
  const { t } = useTranslation();
  const [state, setState] = useState<State>();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const [all, setAll] = useState(false);

  useEffect(() => {
    let disposed = false;
    let timer: number | undefined;
    const poll = async () => {
      if (!document.hidden) {
        try {
          const rsp = await http.request<State>({ method: 'get', url, timeout: 8000 });
          if (!disposed && rsp.code === 0) setState(rsp.data);
        } catch {
          // The dashboard's own notice covers a device that does not answer.
        }
      }
      if (!disposed) timer = window.setTimeout(() => void poll(), 3000);
    };
    void poll();
    return () => {
      disposed = true;
      window.clearTimeout(timer);
    };
  }, []);

  if (!state) return null;
  // Without the right to turn it on there is nothing to show while it is off.
  if (state.mode === 'off' && !admin) return null;

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

  const report = state.status.report;
  const shown = report?.sensors.filter((s) => all || mainKinds.has(s.kind)) ?? [];
  const groups = groupSensors(shown);
  const hint =
    state.mode === 'serial' && state.serial.state === 'unavailable'
      ? t('dashboard.hostSensors.serialUnavailable')
      : state.mode === 'serial' && state.serial.state === 'busy'
        ? t('dashboard.hostSensors.serialBusy')
        : state.mode === 'network' && state.network.addresses.length === 0
          ? t('dashboard.hostSensors.networkUnavailable')
          : t('dashboard.hostSensors.waiting');

  return (
    <Panel className="min-w-0">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h3 className="m-0 text-sm font-medium">{t('dashboard.hostSensors.title')}</h3>
        {admin && (
          <Select<Mode>
            size="small"
            className="min-w-36"
            aria-label={t('dashboard.hostSensors.title')}
            value={state.mode}
            loading={saving}
            disabled={saving}
            onChange={(mode) => void setMode(mode)}
            options={(['off', 'serial', 'network'] as const).map((mode) => ({
              value: mode,
              label: t(`dashboard.hostSensors.mode.${mode}`)
            }))}
          />
        )}
      </div>
      {failed && (
        <Alert
          className="mb-3"
          type="error"
          showIcon
          message={t('dashboard.hostSensors.saveFailed')}
        />
      )}
      {!report && (
        <div className="text-fg-muted space-y-2 text-xs">
          <p className="m-0">{t('dashboard.hostSensors.about')}</p>
          {state.mode !== 'off' && <p className="text-fg m-0">{hint}</p>}
          {state.mode === 'serial' && (
            <p className="m-0">
              {t('dashboard.hostSensors.serialNote', { port: state.serial.port })}
            </p>
          )}
          {state.agent.available && (
            <a href={state.agent.path} download>
              {t('dashboard.hostSensors.download')}
            </a>
          )}
        </div>
      )}
      {report && (
        <>
          <div className="text-fg-muted mb-3 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
            <span className="text-fg wrap-break-word">
              {[report.host, report.os].filter(Boolean).join(' · ')}
            </span>
            <span>{t(`dashboard.hostSensors.mode.${state.status.transport}`)}</span>
            <StatusBadge tone={state.status.stale ? 'danger' : 'success'}>
              {state.status.stale
                ? t('dashboard.hostSensors.stale')
                : t('dashboard.hostSensors.age', {
                    seconds: Math.round(state.status.age / 1000)
                  })}
            </StatusBadge>
          </div>
          {report.limited && (
            <Alert
              className="mb-3"
              type="info"
              showIcon
              message={t('dashboard.hostSensors.limited')}
            />
          )}
          <div className="grid gap-x-6 gap-y-4 md:grid-cols-2">
            {groups.map((group) => (
              <div key={group.hardware} className="min-w-0 text-sm">
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
              {all
                ? t('dashboard.hostSensors.showMain')
                : t('dashboard.hostSensors.showAll', { count: report.sensors.length })}
            </button>
          )}
        </>
      )}
    </Panel>
  );
};
