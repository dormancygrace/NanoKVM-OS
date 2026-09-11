import { useEffect, useRef, useState } from 'react';

import { getKeyboardLedStatus } from '@/api/hid.ts';
import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { client } from '@/lib/websocket.ts';

import {
  KEYBOARD_LED_STATUS_EVENT,
  KeyboardLedStatus,
  parseKeyboardLedStatus,
  parseKeyboardLedStatusMessage,
  shouldAcceptKeyboardLedStatus
} from './model';

export function useKeyboardLedStatus() {
  const [status, setStatus] = useState<KeyboardLedStatus | null>(null);
  const latestStatusRef = useRef<KeyboardLedStatus | null>(null);

  useEffect(() => {
    let disposed = false;

    function update(next: KeyboardLedStatus) {
      if (!shouldAcceptKeyboardLedStatus(latestStatusRef.current, next)) {
        if (latestStatusRef.current && next.keyboardEnabled !== undefined) {
          const merged = { ...latestStatusRef.current, keyboardEnabled: next.keyboardEnabled };
          latestStatusRef.current = merged;
          setStatus(merged);
        }
        return;
      }

      latestStatusRef.current = next;
      setStatus(next);
    }

    const unsubscribe = client.on(KEYBOARD_LED_STATUS_EVENT, (message) => {
      const next = parseKeyboardLedStatusMessage(message);
      if (next) {
        update(next);
      }
    });

    function refresh() {
      getKeyboardLedStatus()
        .then((rsp) => {
          if (rsp.code !== 0) {
            return;
          }

          const next = parseKeyboardLedStatus(rsp.data);
          if (!disposed && next) {
            update(next);
          }
        })
        .catch(() => undefined);
    }
    refresh();
    const interval = window.setInterval(refresh, 10000);
    window.addEventListener(usbCompositionChangedEvent, refresh);

    return () => {
      disposed = true;
      window.clearInterval(interval);
      window.removeEventListener(usbCompositionChangedEvent, refresh);
      unsubscribe();
    };
  }, []);

  return status;
}
