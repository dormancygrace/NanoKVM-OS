import { useEffect, useState, useSyncExternalStore } from 'react';
import { useSetAtom } from 'jotai';
import { UsbIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getVirtualDevice, usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { normalizeUsbStatus, usbDevices } from '@/lib/usb-composition.ts';
import { client } from '@/lib/websocket.ts';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { MenuItem } from '@/components/menu-item.tsx';

import { Usb } from './settings/usb';

export const UsbMenu = () => {
  const { t } = useTranslation();
  const lock = useSetAtom(keyboardLockAtom);
  const [open, setOpen] = useState(false);
  const [usbEnabled, setUsbEnabled] = useState<boolean | null>(null);
  const status = useSyncExternalStore(client.subscribeConnectionStatus, client.getConnectionStatus);
  useEffect(() => {
    let active = true;
    let generation = 0;
    const refresh = async () => {
      const request = ++generation;
      try {
        const response = await getVirtualDevice();
        if (!active || request !== generation || response.code !== 0) return;
        const composition = normalizeUsbStatus(response.data);
        if (usbDevices.every((name) => typeof composition[name] === 'boolean'))
          setUsbEnabled(usbDevices.some((name) => composition[name]));
      } catch {
        // Keep the last confirmed USB state while the connection recovers.
      }
    };
    void refresh();
    window.addEventListener(usbCompositionChangedEvent, refresh);
    return () => {
      active = false;
      window.removeEventListener(usbCompositionChangedEvent, refresh);
    };
  }, [status, open]);
  const usbOff = usbEnabled === false;
  const warning = status === 'disconnected' || status === 'reconnecting';
  const statusText = t(`inputConnection.${status}`);
  const color =
    status === 'disconnected' ? '#ef4444' : status === 'reconnecting' ? '#f59e0b' : undefined;
  const title = `${t('settings.usb.title')}${usbOff ? ` — ${t('settings.usb.off')}` : ''}${warning ? ` — ${statusText}` : ''}`;
  useEffect(() => () => lock({ source: 'usb-popover', locked: false }), [lock]);
  return (
    <MenuItem
      title={title}
      icon={
        <span
          className="relative inline-flex"
          style={{ color, opacity: usbOff && !warning ? 0.55 : 1 }}
          aria-hidden="true"
        >
          <UsbIcon size={18} />
          {usbOff && (
            <svg
              className="absolute inset-0"
              width={18}
              height={18}
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth={2}
              strokeLinecap="round"
            >
              <path d="m3 3 18 18" />
            </svg>
          )}
        </span>
      }
      fresh
      onOpenChange={(value) => {
        setOpen(value);
        lock({ source: 'usb-popover', locked: value });
      }}
      content={
        <div className="max-h-[75vh] w-[min(440px,85vw)] overflow-y-auto p-1">
          {warning && (
            <div role="status" className="mb-2 px-2 text-sm" style={{ color }}>
              {statusText}
            </div>
          )}
          {open && <Usb />}
        </div>
      }
    />
  );
};
