import { useEffect, useState } from 'react';
import { Alert, message } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { formatVersion } from '@/lib/version';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

import { APKUpdates, type APKState } from './apk';

type State = {
  apk?: APKState;
  installed: { version: string };
};

export const Updates = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<State | null>(null);
  const [busy, setBusy] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);

  async function refresh() {
    try {
      const rsp = await http.get('/api/os/update');
      if (rsp.code === 0) {
        setState(rsp.data);
        setReconnecting(false);
      }
    } catch {
      setReconnecting(true);
    }
  }
  useEffect(() => {
    void refresh();
    const stopPolling = pollWhileVisible(refresh, 3000);
    return () => stopPolling();
  }, []);

  async function action(name: string, data?: unknown) {
    setBusy(true);
    try {
      const rsp = await http.post(`/api/os/update/${name}`, data);
      if (rsp.code !== 0) message.error(rsp.msg);
      await refresh();
    } catch {
      message.error(t('settings.updates.requestFailed'));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-6">
      <p className="text-fg-muted mt-0 text-sm">{t('settings.updates.description')}</p>
      <SettingRow label={t('settings.updates.installed')}>
        <span>{state?.installed.version ? formatVersion(state.installed.version) : '—'}</span>
      </SettingRow>
      {state && <APKUpdates state={state.apk} busy={busy} action={action} />}
      {reconnecting && <Alert type="info" message={t('settings.updates.reconnecting')} />}
    </div>
  );
};
