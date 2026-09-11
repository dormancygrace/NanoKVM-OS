import { useEffect } from 'react';
import { atom, useAtom } from 'jotai';

import { defaultTimePreferences, type TimePreferences } from '@/lib/date-time.ts';
import { http } from '@/lib/http.ts';

export const timePreferencesAtom = atom<TimePreferences>(defaultTimePreferences);

export function useDeviceTime() {
  const [preferences, setPreferences] = useAtom(timePreferencesAtom);
  useEffect(() => {
    let mounted = true;
    const refresh = () => {
      void http
        .get('/api/vm/date-time')
        .then((rsp) => {
          if (mounted && rsp.code === 0) setPreferences(rsp.data.config);
        })
        .catch(() => {
          /* Keep the last known format during a disconnect. */
        });
    };
    refresh();
    const timer = window.setInterval(refresh, 60000);
    return () => {
      mounted = false;
      window.clearInterval(timer);
    };
  }, [setPreferences]);
  return preferences;
}
