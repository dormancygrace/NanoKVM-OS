import { useEffect, useState } from 'react';
import { Alert, Tag } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/virtual-device.ts';
import type { UsbHostDevice, UsbHostDevices as UsbHostState } from '@/api/virtual-device.ts';
import { Panel } from '@/components/ui/settings.tsx';

const refreshMs = 3000;

// USB speed as the kernel reports it, in Mbit/s.
function speedKey(speed: string) {
  if (speed === '1.5') return 'low';
  if (speed === '12') return 'full';
  if (speed === '480') return 'high';
  return '';
}

function deviceName(device: UsbHostDevice) {
  return [device.manufacturer, device.product].filter(Boolean).join(' ');
}

// The devices attached to the NanoKVM while its port is a host.
export const UsbHostDevices = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<UsbHostState>();
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let active = true;
    const load = () =>
      api
        .getUsbHostDevices()
        .then((rsp) => {
          if (!active) return;
          if (rsp.code === 0) {
            setState(rsp.data);
            setFailed(false);
          } else {
            setFailed(true);
          }
        })
        .catch(() => {
          if (active) setFailed(true);
        });
    void load();
    const timer = window.setInterval(load, refreshMs);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);

  const devices = state?.devices ?? [];

  return (
    <div className="space-y-3">
      <div className="text-sm font-medium">{t('settings.usb.host.title')}</div>
      {failed && <Alert type="error" showIcon message={t('settings.usb.host.loadFailed')} />}
      {state && devices.length === 0 && (
        <div className="text-fg-muted text-sm">{t('settings.usb.host.empty')}</div>
      )}
      {devices.map((device) => {
        const speed = speedKey(device.speed);
        return (
          <Panel key={device.port} className="flex items-center justify-between gap-3">
            <div className="min-w-0">
              <div className="truncate text-sm">
                {deviceName(device) || t('settings.usb.host.unnamed')}
              </div>
              <div className="text-fg-muted font-mono text-xs">
                {device.vendorId}:{device.productId} · {t('settings.usb.host.port')} {device.port}
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              {device.maxPower && (
                <Tag bordered={false} className="text-fg-muted m-0 font-mono text-[10px]">
                  {device.maxPower}
                </Tag>
              )}
              {speed && (
                <Tag bordered={false} className="m-0 text-[10px]">
                  {t(`settings.usb.host.speed.${speed}`)}
                </Tag>
              )}
            </div>
          </Panel>
        );
      })}
      {state && (
        <p className="text-fg-muted m-0 text-xs">
          {t(`settings.usb.host.power.${state.portPower}`)}
        </p>
      )}
    </div>
  );
};
