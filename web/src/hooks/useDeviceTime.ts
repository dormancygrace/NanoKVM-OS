import { pollWhileVisible } from '@/lib/visible-poll.ts';
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
          if (!mounted || rsp.code !== 0) return;
          const { timezone, format } = rsp.data.config as TimePreferences;
          // Keep the same object while nothing changed, so readers do not re-render.
          setPreferences((current) =>
            current.timezone === timezone && current.format === format
              ? current
              : { timezone, format }
          );
        })
        .catch(() => {
          /* Keep the last known format during a disconnect. */
        });
    };
    refresh();
    const stopPolling = pollWhileVisible(refresh, 60000);
    return () => {
      mounted = false;
      stopPolling();
    };
  }, [setPreferences]);
  return preferences;
}
