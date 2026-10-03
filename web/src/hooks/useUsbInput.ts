import { useEffect, useSyncExternalStore } from 'react';
import { useAtom } from 'jotai';

import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { http } from '@/lib/http.ts';
import { noUsbInput, parseUsbInput } from '@/lib/usb-input.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
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
    let active = true;
    let generation = 0;
    async function refresh() {
      const request = ++generation;
      try {
        const response = await http.get('/api/hid/input-status');
        if (active && request === generation)
          setStatus(response.code === 0 ? parseUsbInput(response.data) : noUsbInput);
      } catch {
        if (active && request === generation) setStatus(noUsbInput);
      }
    }
    void refresh();
    const stopPolling = pollWhileVisible(refresh, 5000);
    window.addEventListener(usbCompositionChangedEvent, refresh);
    window.addEventListener('focus', refresh);
    return () => {
      active = false;
      stopPolling();
      window.removeEventListener(usbCompositionChangedEvent, refresh);
      window.removeEventListener('focus', refresh);
    };
  }, [connection, setStatus]);
  return status;
}
