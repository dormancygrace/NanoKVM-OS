import type { WifiStatus } from '@/api/network.ts';

// A radio operation the Wi-Fi page waits for, until a deadline: switching
// Wi-Fi on or off, joining ssid, or leaving the network named leaving.
export type Pending = {
  until: number;
  enabled?: boolean;
  ssid?: string;
  disconnect?: boolean;
  leaving?: string;
};

// Whether a status poll shows the operation done. Disconnect is done once the
// device is off the network it left: it then joins another saved network that
// connects automatically, possibly before the next poll, or idles.
export function operationDone(operation: Omit<Pending, 'until'>, next: WifiStatus) {
  if (operation.disconnect) return !next.connected || next.ssid !== operation.leaving;
  if (operation.ssid !== undefined) return next.connected && next.ssid === operation.ssid;
  return next.enabled === operation.enabled;
}
