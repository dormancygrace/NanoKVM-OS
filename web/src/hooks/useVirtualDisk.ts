import { useEffect, useState } from 'react';

import { getVirtualDevice, usbCompositionChangedEvent } from '@/api/virtual-device.ts';

// Read the applied composition, never an unapplied draft from the settings panel.
export function useVirtualDisk() {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  useEffect(() => {
    let active = true;
    let generation = 0;
    async function refresh() {
      const request = ++generation;
      try {
        const response = await getVirtualDevice();
        if (!active || request !== generation) return;
        setEnabled(
          response.code === 0 && typeof response.data?.disk === 'boolean'
            ? response.data.disk && response.data.mode !== 'hid-only'
            : null
        );
      } catch {
        if (active && request === generation) setEnabled(null);
      }
    }
    function compositionChanged() {
      setEnabled(null);
      void refresh();
    }
    void refresh();
    const timer = window.setInterval(refresh, 5000);
    window.addEventListener(usbCompositionChangedEvent, compositionChanged);
    window.addEventListener('nanokvm:usb-updated', compositionChanged);
    window.addEventListener('focus', refresh);
    return () => {
      active = false;
      window.clearInterval(timer);
      window.removeEventListener(usbCompositionChangedEvent, compositionChanged);
      window.removeEventListener('nanokvm:usb-updated', compositionChanged);
      window.removeEventListener('focus', refresh);
    };
  }, []);
  return enabled;
}
