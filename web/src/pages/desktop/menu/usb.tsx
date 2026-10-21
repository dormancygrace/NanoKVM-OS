import { useContext, useEffect, useState, useSyncExternalStore } from 'react';
import clsx from 'clsx';
import { useSetAtom } from 'jotai';
import { FingerprintIcon, LayersIcon, UsbIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getVirtualDevice, usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { normalizeUsbStatus, usbDevices } from '@/lib/usb-composition.ts';
import { client } from '@/lib/websocket.ts';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { MenuItem, MenuSubmenu } from '@/components/menu-item.tsx';
import { MobileMenuItemContext } from '@/components/mobile-menu-context';

import { Usb } from './settings/usb';

// The USB Gadget menu: the composition, and the identity the computer sees.
const submenus = [
  { part: 'composition', icon: <LayersIcon size={18} />, title: 'settings.usb.compositionMenu' },
  { part: 'identity', icon: <FingerprintIcon size={18} />, title: 'settings.usb.identityMenu' }
] as const;

export const UsbMenu = () => {
  const { t } = useTranslation();
  const { mobilePlacement } = useContext(MobileMenuItemContext);
  const lock = useSetAtom(keyboardLockAtom);
  const [open, setOpen] = useState(false);
  // The open submenu; its panel mounts on opening and reads the USB state then.
  const [submenu, setSubmenu] = useState<(typeof submenus)[number]['part'] | null>(null);
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
  }, [status, open, submenu]);
  const usbOff = usbEnabled === false;
  const warning = status === 'disconnected' || status === 'reconnecting';
  const statusText = t(`inputConnection.${status}`);
  const color =
    status === 'disconnected' ? '#ef4444' : status === 'reconnecting' ? '#f59e0b' : undefined;
  const title = `${t('settings.usb.gadget')}${usbOff ? ` — ${t('settings.usb.off')}` : ''}${warning ? ` — ${statusText}` : ''}`;
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
        if (!value) setSubmenu(null);
        lock({ source: 'usb-popover', locked: value });
      }}
      content={
        <div className="flex flex-col space-y-1">
          {warning && (
            <div role="status" className="mb-1 px-2 text-sm" style={{ color }}>
              {statusText}
            </div>
          )}
          {submenus.map((item) => (
            <MenuSubmenu
              key={item.part}
              title={t(item.title)}
              content={
                submenu === item.part && (
                  <div
                    className={clsx(
                      'p-1',
                      mobilePlacement
                        ? 'box-border w-full min-w-0'
                        : 'max-h-[75dvh] w-[min(440px,85vw)] overflow-y-auto'
                    )}
                  >
                    <Usb part={item.part} />
                  </div>
                )
              }
              popoverProps={{
                placement: 'rightTop',
                arrow: false,
                align: { offset: [14, 0] },
                // a form: it stays open while the pointer leaves it
                trigger: 'click',
                open: submenu === item.part,
                onOpenChange: (value) => setSubmenu(value ? item.part : null)
              }}
            >
              <div className="flex h-[30px] cursor-pointer items-center space-x-2 rounded px-3 text-neutral-300 hover:bg-neutral-700/70">
                {item.icon}
                <span>{t(item.title)}</span>
              </div>
            </MenuSubmenu>
          ))}
        </div>
      }
    />
  );
};
