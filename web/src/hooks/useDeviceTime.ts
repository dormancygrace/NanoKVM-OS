import { useEffect } from 'react';
import { atom, useAtom } from 'jotai';

import type { DateTimeConfig } from '@/lib/date-time.ts';
import { defaultTimePreferences, type TimePreferences } from '@/lib/date-time.ts';
import { http } from '@/lib/http.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';

type Status = { config: DateTimeConfig; zones: string[]; now: number; synchronized: boolean };

export const timePreferencesAtom = atom<TimePreferences>(defaultTimePreferences);

export function useDeviceTime() {
  const [preferences, setPreferences] = useAtom(timePreferencesAtom);
  useEffect(() => {
    let mounted = true;
    const refresh = () => {
      void http
        .get<Status>('/api/vm/date-time')
        .then((rsp) => {
          if (!mounted || rsp.code !== 0) return;
          const { timezone, format, dateFormat = 'auto' } = rsp.data.config as TimePreferences;
          // Keep the same object while nothing changed, so readers do not re-render.
          setPreferences((current) =>
            current.timezone === timezone &&
            current.format === format &&
            current.dateFormat === dateFormat
              ? current
              : { timezone, format, dateFormat }
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
