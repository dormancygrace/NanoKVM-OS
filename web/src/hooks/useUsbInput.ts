import { useEffect, useSyncExternalStore } from 'react';
import { useAtom } from 'jotai';

import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { refreshLiveStatus, subscribeLiveStatus } from '@/lib/live-status.ts';
import { noUsbInput, parseUsbInput } from '@/lib/usb-input.ts';
import { client } from '@/lib/websocket.ts';
import { usbInputAtom } from '@/jotai/usb-input.ts';

// Use applied device state, including changes made in another browser session.
export function useUsbInput() {
  const [status, setStatus] = useAtom(usbInputAtom);
  const connection = useSyncExternalStore(
    client.subscribeConnectionStatus,
    client.getConnectionStatus
  );
  useEffect(() => {
    const refresh = () => void refreshLiveStatus({ force: true });
    const unsubscribe = subscribeLiveStatus((live) =>
      setStatus(live ? parseUsbInput(live.input) : noUsbInput)
    );
    // A new connection may follow a device restart: read again.
    refresh();
    window.addEventListener(usbCompositionChangedEvent, refresh);
    window.addEventListener('focus', refresh);
    return () => {
      unsubscribe();
      window.removeEventListener(usbCompositionChangedEvent, refresh);
      window.removeEventListener('focus', refresh);
    };
  }, [connection, setStatus]);
  return status;
}
