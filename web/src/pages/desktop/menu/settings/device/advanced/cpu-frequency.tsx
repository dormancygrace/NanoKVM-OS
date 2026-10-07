import { useEffect, useState } from 'react';
import { Alert, Checkbox, Select } from 'antd';
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
    applyAtBoot?: boolean;
    bootFallback?: boolean;
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
  async function save(target: number, applyAtBoot: boolean) {
    setLoading(true);
    try {
      const rsp = await api.setCPUFrequency(target, applyAtBoot);
      if (rsp.code !== 0) showRequestError(rsp, 'settings.device.cpuFrequency.failed');
      await refresh();
    } catch (err) {
      showRequestError(err, 'settings.device.cpuFrequency.failed');
    } finally {
      setLoading(false);
    }
  }
  async function update(target: number) {
    const applyAtBoot = !!state?.applyAtBoot;
    if (
      target > 1000 &&
      !(await confirmAction({
        title: t('settings.device.cpuFrequency.confirmOverclock', { mhz: target }),
        content: (
          <>
            <p className="mt-0">{t('settings.device.cpuFrequency.warning')}</p>
            {applyAtBoot && (
              <p className="text-danger mb-0">{t('settings.device.cpuFrequency.bootWarning')}</p>
            )}
          </>
        ),
        danger: true
      }))
    )
      return;
    await save(target, applyAtBoot);
  }
  async function toggleBoot(applyAtBoot: boolean) {
    if (!state) return;
    if (
      applyAtBoot &&
      !(await confirmAction({
        title: t('settings.device.cpuFrequency.confirmBoot'),
        content: t('settings.device.cpuFrequency.bootWarning'),
        danger: true
      }))
    )
      return;
    await save(state.target, applyAtBoot);
  }
  const overclocked = !!state?.supported && Math.max(state.target, state.running) > 1000;
  return (
    <div className="space-y-2">
      {loadError !== null && (
        <Alert
          type="error"
          showIcon
          message={loadError || t('settings.device.cpuFrequency.failed')}
        />
      )}
      {state?.bootFallback && (
        <Alert type="warning" showIcon message={t('settings.device.cpuFrequency.bootFallback')} />
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
        <div className="space-y-1">
          <Checkbox
            checked={!!state.applyAtBoot}
            disabled={loading}
            aria-describedby="device-cpu-frequency-boot-description"
            onChange={(e) => void toggleBoot(e.target.checked)}
          >
            {t('settings.device.cpuFrequency.applyAtBoot')}
          </Checkbox>
          <div id="device-cpu-frequency-boot-description" className="text-danger text-xs">
            {t('settings.device.cpuFrequency.bootWarning')}
          </div>
        </div>
      )}
      {state?.supported && (
        <div className="text-fg-muted text-xs">
          {t(
            state.applyAtBoot
              ? 'settings.device.cpuFrequency.descriptionAtBoot'
              : 'settings.device.cpuFrequency.description'
          )}
        </div>
      )}
      {overclocked && (
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
