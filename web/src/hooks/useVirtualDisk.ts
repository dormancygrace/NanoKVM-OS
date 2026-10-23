import { useEffect, useState } from 'react';

import { usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { refreshLiveStatus, subscribeLiveStatus } from '@/lib/live-status.ts';
import { virtualDiskFromLive } from '@/lib/virtual-disk-state.ts';

// Read the applied composition, never an unapplied draft from the settings panel.
// The live status carries it for administrators only; others get null.
export function useVirtualDisk() {
  const [enabled, setEnabled] = useState<boolean | null>(null);
  useEffect(() => {
    let changedAt = -Infinity;
    const unsubscribe = subscribeLiveStatus((live, startedAt) => {
      if (startedAt < changedAt) return;
      setEnabled((previous) => virtualDiskFromLive(previous, live));
    });
    const refresh = () => void refreshLiveStatus({ force: true });
    function compositionChanged() {
      changedAt = performance.now();
      setEnabled(null);
      refresh();
    }
    window.addEventListener(usbCompositionChangedEvent, compositionChanged);
    window.addEventListener('nanokvm:usb-updated', compositionChanged);
    window.addEventListener('focus', refresh);
    return () => {
      unsubscribe();
      window.removeEventListener(usbCompositionChangedEvent, compositionChanged);
      window.removeEventListener('nanokvm:usb-updated', compositionChanged);
      window.removeEventListener('focus', refresh);
    };
  }, []);
  return enabled;
}
