import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, message, Popconfirm, Select } from 'antd';
import { useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import type { Components, UpdateNotice } from '@/api/updates.ts';
import { http } from '@/lib/http';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { updateNoticeAtom } from '@/jotai/updates.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

import { UpdateCard, type APKState } from './card';
import { ComponentList, Fold } from './components';

type State = {
  apk?: APKState;
  installed: { version: string };
  components?: Components;
  notice?: UpdateNotice;
  channel?: { channel: string; available: boolean; rollbackAvailable?: boolean };
};

// System > Updates: the update card first, then the installed components and
// the update channel, both folded.
export const Updates = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<State | null>(null);
  const [busy, setBusy] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const mounted = useRef(false);
  const generation = useRef(0);
  const pending = useRef(0);
  const setNotice = useSetAtom(updateNoticeAtom);

  const refresh = useCallback(async () => {
    if (!mounted.current) return;
    const request = ++generation.current;
    pending.current++;
    try {
      const rsp = await http.get<State>('/api/os/update');
      if (request !== generation.current) return;
      if (rsp.code === 0) {
        setState(rsp.data);
        // Keep the settings badge in step with what this page shows.
        if (rsp.data.notice) setNotice(rsp.data.notice);
        setReconnecting(false);
      }
    } catch {
      if (request === generation.current) setReconnecting(true);
    } finally {
      pending.current--;
    }
  }, [setNotice]);
  useEffect(() => {
    const requests = generation;
    mounted.current = true;
    void refresh();
    const stopPolling = pollWhileVisible(() => {
      if (pending.current === 0) void refresh();
    }, 3000);
    return () => {
      mounted.current = false;
      requests.current++;
      stopPolling();
    };
  }, [refresh]);

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

  const working = busy || ['checking', 'installing', 'rebooting'].includes(state?.apk?.state || '');
  return (
    <div className="space-y-4">
      <p className="text-fg-muted mt-0 text-sm">{t('settings.updates.description')}</p>
      {reconnecting && <Alert type="info" title={t('settings.updates.reconnecting')} />}
      {state?.channel?.channel === 'beta' && (
        <Alert type="warning" title={t('settings.updates.channel.warning')} />
      )}
      {state?.channel?.rollbackAvailable && (
        <Alert
          type="warning"
          title={t('settings.updates.channel.rollbackDescription')}
          action={
            <Popconfirm
              title={t('settings.updates.channel.rollbackConfirm')}
              onConfirm={() => action('apk/rollback')}
              disabled={working}
            >
              <Button disabled={working}>{t('settings.updates.channel.rollback')}</Button>
            </Popconfirm>
          }
        />
      )}
      {state && (
        <UpdateCard
          version={state.installed.version}
          apk={state.apk}
          notice={state.notice}
          components={state.components}
          busy={busy}
          action={action}
          onChange={refresh}
        />
      )}
      {state && (
        <Fold title={t('settings.updates.components.title')}>
          <ComponentList components={state.components} />
        </Fold>
      )}
      {state?.channel && (
        <Fold title={t('settings.updates.advanced')}>
          <SettingRow
            label={t('settings.updates.channel.title')}
            description={t('settings.updates.channel.description')}
          >
            <Select
              aria-label={t('settings.updates.channel.title')}
              value={state.channel.channel}
              disabled={!state.channel.available || working}
              options={
                state.channel.available
                  ? [
                      { value: 'stable', label: t('settings.updates.channel.stable') },
                      { value: 'beta', label: t('settings.updates.channel.beta') }
                    ]
                  : [{ value: 'custom', label: t('settings.updates.channel.custom') }]
              }
              onChange={(channel) => action('channel', { channel })}
              className="min-w-36"
            />
          </SettingRow>
        </Fold>
      )}
    </div>
  );
};
