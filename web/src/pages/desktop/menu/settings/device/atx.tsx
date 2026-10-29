import { useEffect, useState } from 'react';
import { Alert, Switch } from 'antd';
import { useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { atxEnabledAtom } from '@/jotai/atx.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

const KEY_FAILED = 'settings.device.atx.failed';

// The host's power and reset buttons and its power LED: through the KVM-B
// board on a Cube or Lite, on the card for PCIe. Off: no power button on the
// toolbar, no LED monitor, and Redfish, IPMI and MCP see no buttons.
export const Atx = () => {
  const { t } = useTranslation();
  const setAtxEnabled = useSetAtom(atxEnabledAtom);
  const [state, setState] = useState<api.ATXState>();
  const [busy, setBusy] = useState(false);
  const [loadError, setLoadError] = useState(false);

  useEffect(() => {
    let disposed = false;
    api
      .getATX()
      .then((rsp) => {
        if (disposed) return;
        if (rsp.code !== 0) setLoadError(true);
        else setState(rsp.data as api.ATXState);
      })
      .catch(() => {
        if (!disposed) setLoadError(true);
      });
    return () => {
      disposed = true;
    };
  }, []);

  async function toggle(enabled: boolean) {
    if (!state || busy) return;
    setBusy(true);
    try {
      const rsp = await api.setATX(enabled);
      if (rsp.code !== 0) {
        showRequestError(rsp, KEY_FAILED);
        return;
      }
      setState({ ...state, enabled: rsp.data.enabled });
      setAtxEnabled(rsp.data.enabled);
    } catch (err) {
      showRequestError(err, KEY_FAILED);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      {loadError && <Alert type="error" showIcon message={t(KEY_FAILED)} />}
      <SettingRow
        label={t('settings.device.atx.title')}
        description={
          state && !state.available
            ? t('settings.device.atx.unavailable')
            : t('settings.device.atx.description')
        }
        htmlFor="device-atx"
      >
        <Switch
          id="device-atx"
          aria-describedby="device-atx-description"
          checked={!!state?.enabled}
          loading={!state || busy}
          disabled={!state?.available || busy}
          onChange={(enabled) => void toggle(enabled)}
        />
      </SettingRow>
    </div>
  );
};
