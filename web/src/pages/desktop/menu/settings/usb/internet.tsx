import { useCallback, useEffect, useState } from 'react';
import { message, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import {
  getUsbInternet,
  setUsbHostAccess,
  setUsbInternet,
  usbCompositionChangedEvent
} from '@/api/virtual-device';
import type { UsbInternetStatus } from '@/api/virtual-device';

// Sharing needs the USB network function. Without it the switch is shown
// only while sharing is still on, so that it can be turned off. The same
// holds for the computer's access to the NanoKVM over USB.
export const UsbInternet = ({ usbBusy, network }: { usbBusy: boolean; network: boolean }) => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<UsbInternetStatus>();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    const rsp = await getUsbInternet();
    if (rsp.code !== 0) throw new Error(rsp.msg);
    return rsp.data;
  }, []);

  useEffect(() => {
    let active = true;
    const load = () => {
      void refresh()
        .then((next) => {
          if (active) {
            setStatus(next);
            setFailed(false);
          }
        })
        .catch(() => {
          if (active) setFailed(true);
        });
    };
    load();
    const timer = window.setInterval(load, 5000);
    window.addEventListener(usbCompositionChangedEvent, load);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener(usbCompositionChangedEvent, load);
    };
  }, [refresh]);

  async function toggle(setting: 'enabled' | 'hostAccess', enabled: boolean) {
    if (!status || saving || usbBusy) return;
    const set = setting === 'enabled' ? setUsbInternet : setUsbHostAccess;
    const failure = t(
      setting === 'enabled'
        ? 'settings.usb.internet.updateFailed'
        : 'settings.usb.internet.hostAccess.updateFailed'
    );
    setSaving(true);
    try {
      const rsp = await set(enabled, status[setting]);
      if (rsp.code !== 0) {
        message.error(failure);
        setStatus(await refresh());
      } else setStatus(rsp.data);
    } catch {
      // Read back after a lost response; do not replay the mutation.
      try {
        const next = await refresh();
        setStatus(next);
        if (next[setting] !== enabled) message.error(failure);
      } catch {
        setFailed(true);
        message.error(failure);
      }
    } finally {
      setSaving(false);
    }
  }

  if (!network && !status?.enabled && !status?.hostAccess) return null;
  const state = failed ? 'unavailable' : (status?.state ?? 'loading');
  return (
    <div className="mb-6 space-y-2">
      <div className="flex items-center justify-between gap-3">
        <span>{t('settings.usb.internet.title')}</span>
        <Switch
          aria-label={t('settings.usb.internet.title')}
          checked={Boolean(status?.enabled)}
          disabled={!status || usbBusy || saving || failed}
          loading={saving || (!status && !failed)}
          onChange={(value) => void toggle('enabled', value)}
        />
      </div>
      <p className="text-fg-muted text-xs">{t('settings.usb.internet.description')}</p>
      <div role="status" aria-live="polite" className="text-fg-muted text-xs">
        {t(`settings.usb.internet.states.${state}`, {
          defaultValue: t('settings.usb.internet.states.error'),
          uplink: status?.uplink,
          address: status?.address
        })}
      </div>
      {state === 'active' && (
        <div className="text-fg-muted text-xs">
          {t(
            status?.flowOffload
              ? 'settings.usb.internet.offload'
              : 'settings.usb.internet.noOffload'
          )}
        </div>
      )}
      <p className="text-fg-muted text-xs">{t('settings.usb.internet.renew')}</p>
      <div className="flex items-center justify-between gap-3 pt-2">
        <span>{t('settings.usb.internet.hostAccess.title')}</span>
        <Switch
          aria-label={t('settings.usb.internet.hostAccess.title')}
          checked={Boolean(status?.hostAccess)}
          disabled={!status || usbBusy || saving || failed}
          loading={saving || (!status && !failed)}
          onChange={(value) => void toggle('hostAccess', value)}
        />
      </div>
      <p className="text-fg-muted text-xs">{t('settings.usb.internet.hostAccess.description')}</p>
    </div>
  );
};
