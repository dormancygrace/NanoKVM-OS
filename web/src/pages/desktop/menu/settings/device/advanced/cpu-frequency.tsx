import { useEffect, useState } from 'react';
import { Select } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

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
  const [error, setError] = useState('');
  function refresh() {
    return api.getCPUFrequency().then((rsp) => {
      if (rsp.code === 0) setState(rsp.data);
      else setError(rsp.msg);
    });
  }
  useEffect(() => {
    const poll = () => refresh().catch(() => setError(t('settings.device.cpuFrequency.failed')));
    poll();
    const timer = window.setInterval(poll, 2000);
    return () => window.clearInterval(timer);
  }, []);
  async function update(target: number) {
    setLoading(true);
    setError('');
    try {
      const rsp = await api.setCPUFrequency(target);
      if (rsp.code !== 0) setError(rsp.msg);
      await refresh();
    } catch {
      setError(t('settings.device.cpuFrequency.failed'));
    } finally {
      setLoading(false);
    }
  }
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-4">
        <div>
          <div>{t('settings.device.cpuFrequency.title')}</div>
          <div className="text-xs text-neutral-500">
            {state?.supported
              ? t('settings.device.cpuFrequency.running', { mhz: state.running })
              : t('settings.device.cpuFrequency.unavailable')}
          </div>
        </div>
        <Select
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
      </div>
      {state?.supported && (
        <div className="text-xs text-neutral-500">
          {t('settings.device.cpuFrequency.description')}
        </div>
      )}
      {state?.supported && state.options.some((value) => value > 1000) && (
        <div className="text-xs text-amber-500" role="note">
          {t('settings.device.cpuFrequency.warning')}
        </div>
      )}
      {state?.throttled && (
        <div className="text-xs text-amber-500" role="status">
          {t('settings.device.cpuFrequency.throttled')}
        </div>
      )}
      {error && <div className="text-xs text-red-400">{error}</div>}
    </div>
  );
};
