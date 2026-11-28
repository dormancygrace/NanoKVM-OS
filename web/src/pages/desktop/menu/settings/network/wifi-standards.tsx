import { useState } from 'react';
import { Alert, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import { startNetworkTrial } from '@/lib/network-trial.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

// Wi-Fi 5 (802.11ac) and Wi-Fi 6 (802.11ax) of the AIC8801. Its Wi-Fi 6 links
// stall transmissions for seconds, so both are off unless turned on here.
// Changing them reloads the Wi-Fi driver, so it asks first.
export const WifiStandards = ({
  state,
  value,
  setValue,
  locked,
  onRestarting
}: {
  state: api.WifiStatus;
  value: api.WifiStandards;
  setValue: (value: api.WifiStandards) => void;
  locked: boolean;
  onRestarting?: () => void;
}) => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.network.wifi.${key}`);
  const [saving, setSaving] = useState(false);

  async function change(wifi5: boolean, wifi6: boolean) {
    if (saving || locked) return;
    // Wi-Fi 6 on 5 GHz needs the Wi-Fi 5 capabilities.
    const wifi6On = wifi5 && wifi6;
    if (wifi5 === value.wifi5 && wifi6On === value.wifi6) return;
    // The driver loaded (Wi-Fi on, or Bluetooth on it) reloads now.
    const reloads = !!value.active;
    if (
      reloads &&
      !(await confirmAction({
        title: tr('standardsConfirm'),
        content: (
          <div className="space-y-2">
            <p>{tr('standardsRestart')}</p>
            {state.thisConnection && (
              <Alert type="warning" showIcon message={tr('countryOverWifi')} />
            )}
          </div>
        ),
        okText: tr('confirmBtn'),
        danger: !!state.thisConnection
      }))
    )
      return;
    setSaving(true);
    try {
      const rsp = await api.setWifiStandards(wifi5, wifi6On);
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.wifi.operationFailed');
        return;
      }
      const { trial, ...reply } = rsp.data;
      // The reply's active is the driver before its reload, which follows the
      // reply: the driver comes back with the saved standards. A reload that
      // fails shows again on the next read.
      setValue(reloads ? { ...reply, active: { wifi5: reply.wifi5, wifi6: reply.wifi6 } } : reply);
      if (trial) startNetworkTrial(trial);
      else if (reloads && state.enabled) onRestarting?.();
    } catch (err) {
      showRequestError(err, 'settings.network.wifi.operationFailed');
    } finally {
      setSaving(false);
    }
  }

  // Saved but not yet what the driver runs with (it loads next with them).
  const pending =
    !!value.active && (value.active.wifi5 !== value.wifi5 || value.active.wifi6 !== value.wifi6);
  return (
    <>
      <SettingRow label={tr('wifi5')} description={tr('wifi5Hint')} htmlFor="wifi-standard-5">
        <Switch
          id="wifi-standard-5"
          aria-describedby="wifi-standard-5-description"
          checked={value.wifi5}
          loading={saving}
          disabled={saving || locked}
          onChange={(on) => void change(on, on && value.wifi6)}
        />
      </SettingRow>
      <SettingRow label={tr('wifi6')} description={tr('wifi6Hint')} htmlFor="wifi-standard-6">
        <Switch
          id="wifi-standard-6"
          aria-describedby="wifi-standard-6-description"
          checked={value.wifi6}
          loading={saving}
          disabled={saving || locked || !value.wifi5}
          onChange={(on) => void change(value.wifi5, on)}
        />
      </SettingRow>
      {pending && (
        <div role="status" className="text-fg-muted text-xs">
          {tr('standardsPending')}
        </div>
      )}
    </>
  );
};
