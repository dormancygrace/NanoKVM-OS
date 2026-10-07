import { useEffect, useState } from 'react';
import { Alert, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { SettingRow, StatusBadge } from '@/components/ui/settings.tsx';

export const CPUFrequency = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<{
    supported: boolean;
    throttled?: boolean;
    running: number;
    target: number;
    options: number[];
  }>();
  const [loading, setLoading] = useState(false);
  // null: no error; '': failed without a server message.
  const [loadError, setLoadError] = useState<string | null>(null);
  function refresh() {
    return api.getCPUFrequency().then((rsp) => {
      if (rsp.code === 0) {
        setState(rsp.data);
        setLoadError(null);
      } else setLoadError(rsp.msg ?? '');
    });
  }
  useEffect(() => {
    const poll = () => refresh().catch(() => setLoadError(''));
    poll();
    const stopPolling = pollWhileVisible(poll, 2000);
    return () => stopPolling();
  }, []);
  async function update(target: number) {
    if (
      target > 1000 &&
      !(await confirmAction({
        title: t('settings.device.cpuFrequency.confirmOverclock', { mhz: target }),
        content: t('settings.device.cpuFrequency.warning'),
        danger: true
      }))
    )
      return;
    setLoading(true);
    try {
      const rsp = await api.setCPUFrequency(target);
      if (rsp.code !== 0) showRequestError(rsp, 'settings.device.cpuFrequency.failed');
      await refresh();
    } catch (err) {
      showRequestError(err, 'settings.device.cpuFrequency.failed');
    } finally {
      setLoading(false);
    }
  }
  return (
    <div className="space-y-2">
      {loadError !== null && (
        <Alert
          type="error"
          showIcon
          message={loadError || t('settings.device.cpuFrequency.failed')}
        />
      )}
      <SettingRow
        label={t('settings.device.cpuFrequency.title')}
        description={
          state?.supported
            ? t('settings.device.cpuFrequency.running', { mhz: state.running })
            : t('settings.device.cpuFrequency.unavailable')
        }
        htmlFor="device-cpu-frequency"
      >
        <Select
          id="device-cpu-frequency"
          aria-describedby="device-cpu-frequency-description"
          style={{ width: 280, maxWidth: '100%' }}
          disabled={!state?.supported || loading}
          loading={loading}
          value={state?.supported ? state.target : undefined}
          options={state?.options.map((value) => ({
            value,
            label: `${value} MHz — ${t(`settings.device.cpuFrequency.${value === 850 ? 'eco' : value === 1000 ? 'stock' : value <= 1100 ? 'moderate' : 'sampleDependent'}`)}`
          }))}
          onChange={update}
        />
      </SettingRow>
      {state?.supported && (
        <div className="text-fg-muted text-xs">{t('settings.device.cpuFrequency.description')}</div>
      )}
      {state?.supported && state.options.some((value) => value > 1000) && (
        <div className="text-warning text-xs" role="note">
          {t('settings.device.cpuFrequency.warning')}
        </div>
      )}
      {state?.throttled && (
        <div role="status">
          <StatusBadge tone="warning">{t('settings.device.cpuFrequency.throttled')}</StatusBadge>
        </div>
      )}
    </div>
  );
};
