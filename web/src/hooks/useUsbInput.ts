import { useEffect, useState, useSyncExternalStore } from 'react';

import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { http } from '@/lib/http.ts';
import { client } from '@/lib/websocket.ts';

// Use applied device state, including changes made in another browser session.
export function useUsbInput() {
  const [available, setAvailable] = useState(false);
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
          setAvailable(response.code === 0 && response.data?.available === true);
      } catch {
        if (active && request === generation) setAvailable(false);
      }
    }
    void refresh();
    const timer = window.setInterval(refresh, 5000);
    window.addEventListener(usbCompositionChangedEvent, refresh);
    window.addEventListener('focus', refresh);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener(usbCompositionChangedEvent, refresh);
      window.removeEventListener('focus', refresh);
    };
  }, [connection]);
  return available;
}
