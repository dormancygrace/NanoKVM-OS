import { useEffect, useState } from 'react';
import { Alert, Spin } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { formatDeviceTime } from '@/lib/date-time.ts';
import { http } from '@/lib/http';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { timePreferencesAtom } from '@/hooks/useDeviceTime.ts';
import { StatusBadge } from '@/components/ui/settings.tsx';

type Entry = {
  seq: number;
  time: string;
  kind: string;
  severity: 'info' | 'warning' | 'critical';
  title: string;
  body: string;
};

const tones = { info: 'neutral', warning: 'warning', critical: 'danger' } as const;

// System > Event log: what happened to the host and who did what to it, as
// the server's journal keeps it (server/journal), newest first.
export const Events = () => {
  const { t, i18n } = useTranslation('translation', { keyPrefix: 'settings.events' });
  const timePreferences = useAtomValue(timePreferencesAtom);
  const [entries, setEntries] = useState<Entry[]>();
  const [kept, setKept] = useState(0);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let disposed = false;
    const poll = async () => {
      try {
        const rsp = await http.request<{ entries: Entry[]; kept: number }>({
          method: 'get',
          url: '/api/vm/journal',
          params: { lang: i18n.language },
          timeout: 8000
        });
        if (disposed) return;
        if (rsp.code === 0) {
          setEntries(rsp.data.entries);
          setKept(rsp.data.kept);
        }
        setFailed(rsp.code !== 0);
      } catch {
        if (!disposed) setFailed(true);
      }
    };
    void poll();
    const stop = pollWhileVisible(() => void poll(), 10000);
    return () => {
      disposed = true;
      stop();
    };
  }, [i18n.language]);

  return (
    <div className="flex flex-col gap-4">
      <p className="text-fg-muted m-0 text-sm">{t('description', { count: kept || 500 })}</p>
      {failed && <Alert type="error" title={t('failed')} showIcon />}
      {!entries && !failed && <Spin />}
      {entries?.length === 0 && <p className="text-fg-muted m-0 text-sm">{t('empty')}</p>}
      {entries && entries.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-2 p-0">
          {entries.map((entry) => (
            <li key={entry.seq} className="border-border flex flex-col gap-1 border-b pb-2">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge tone={tones[entry.severity] ?? 'neutral'}>{entry.title}</StatusBadge>
                <span className="text-fg-muted text-xs">
                  {formatDeviceTime(new Date(entry.time).getTime(), timePreferences)}
                </span>
              </div>
              {entry.body && (
                <span className="text-sm break-words whitespace-pre-wrap">{entry.body}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
};
