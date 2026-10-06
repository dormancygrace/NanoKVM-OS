import { useCallback, useEffect, useState } from 'react';
import { message, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { getUsbInternet, setUsbInternet, usbCompositionChangedEvent } from '@/api/virtual-device';
import type { UsbInternetStatus } from '@/api/virtual-device';

export const UsbInternet = ({ usbBusy }: { usbBusy: boolean }) => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<UsbInternetStatus>();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    const rsp = await getUsbInternet();
    if (rsp.code !== 0) throw new Error(rsp.msg);
    return rsp.data as UsbInternetStatus;
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

  async function toggle(enabled: boolean) {
    if (!status || saving || usbBusy) return;
    setSaving(true);
    try {
      const rsp = await setUsbInternet(enabled, status.enabled);
      if (rsp.code !== 0) {
        message.error(t('settings.usb.internet.updateFailed'));
        setStatus(await refresh());
      } else setStatus(rsp.data);
    } catch {
      // Read back after a lost response; do not replay the mutation.
      try {
        const next = await refresh();
        setStatus(next);
        if (next.enabled !== enabled) message.error(t('settings.usb.internet.updateFailed'));
      } catch {
        setFailed(true);
        message.error(t('settings.usb.internet.updateFailed'));
      }
    } finally {
      setSaving(false);
    }
  }

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
          onChange={(value) => void toggle(value)}
        />
      </div>
      <p className="text-xs text-neutral-500">{t('settings.usb.internet.description')}</p>
      <div role="status" aria-live="polite" className="text-xs text-neutral-400">
        {t(`settings.usb.internet.states.${state}`, {
          defaultValue: t('settings.usb.internet.states.error'),
          uplink: status?.uplink,
          address: status?.address
        })}
      </div>
      {state === 'active' && (
        <div className="text-xs text-neutral-500">
          {t(
            status?.flowOffload
              ? 'settings.usb.internet.offload'
              : 'settings.usb.internet.noOffload'
          )}
        </div>
      )}
      <p className="text-xs text-neutral-500">{t('settings.usb.internet.renew')}</p>
    </div>
  );
};
