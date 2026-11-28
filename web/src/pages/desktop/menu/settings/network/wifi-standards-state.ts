import { useCallback, useEffect, useState } from 'react';

import * as api from '@/api/network.ts';
import { networkTrialDone, type NetworkTrialResult } from '@/lib/network-trial.ts';

// The saved Wi-Fi 5 / Wi-Fi 6 setting of the AIC8801 and what its driver runs
// with, read again after a Wi-Fi restart or an undone trial.
export const useWifiStandards = (state?: api.WifiStatus) => {
  const [value, setValue] = useState<api.WifiStandards>();
  const load = useCallback(async () => {
    try {
      const rsp = await api.getWifiStandards();
      if (rsp.code === 0) setValue(rsp.data);
    } catch {
      // The status poll reports a dead device.
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load, state?.enabled, state?.busy]);
  // A trial that was not kept brought the previous standards back: show them.
  useEffect(() => {
    const done = (event: Event) => {
      if ((event as CustomEvent<NetworkTrialResult>).detail !== 'kept') void load();
    };
    window.addEventListener(networkTrialDone, done);
    return () => window.removeEventListener(networkTrialDone, done);
  }, [load]);
  return [value, setValue] as const;
};
