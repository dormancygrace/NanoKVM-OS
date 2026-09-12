import { useEffect, useState, useSyncExternalStore } from 'react';
import { useSetAtom } from 'jotai';
import { UsbIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { client } from '@/lib/websocket.ts';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { MenuItem } from '@/components/menu-item.tsx';

import { Usb } from './settings/usb';

export const UsbMenu = () => {
  const { t } = useTranslation();
  const lock = useSetAtom(keyboardLockAtom);
  const [open, setOpen] = useState(false);
  const status = useSyncExternalStore(client.subscribeConnectionStatus, client.getConnectionStatus);
  const warning = status === 'disconnected' || status === 'reconnecting';
  const statusText = t(`inputConnection.${status}`);
  const color =
    status === 'disconnected' ? '#ef4444' : status === 'reconnecting' ? '#f59e0b' : undefined;
  useEffect(() => () => lock({ source: 'usb-popover', locked: false }), [lock]);
  return (
    <MenuItem
      title={warning ? `${t('settings.usb.title')} — ${statusText}` : t('settings.usb.title')}
      icon={<UsbIcon size={18} style={{ color }} />}
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
