import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, message, Popconfirm, Select } from 'antd';
import { useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import type { Components, UpdateNotice } from '@/api/updates.ts';
import { http } from '@/lib/http';
import { formatVersion } from '@/lib/version';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { updateNoticeAtom } from '@/jotai/updates.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

import { APKUpdates, type APKState } from './apk';
import { ComponentList, UpdateCheck } from './components';

type State = {
  apk?: APKState;
  installed: { version: string };
  components?: Components;
  notice?: UpdateNotice;
  channel?: { channel: string; available: boolean; rollbackAvailable?: boolean };
};

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
  return (
    <div className="space-y-6">
      <p className="text-fg-muted mt-0 text-sm">{t('settings.updates.description')}</p>
      <SettingRow label={t('settings.updates.installed')}>
        <span>{state?.installed.version ? formatVersion(state.installed.version) : '—'}</span>
      </SettingRow>
      {state?.channel && (
        <SettingRow
          label={t('settings.updates.channel.title')}
          description={t('settings.updates.channel.description')}
        >
          <Select
            aria-label={t('settings.updates.channel.title')}
            value={state.channel.channel}
            disabled={
              !state.channel.available ||
              busy ||
              ['checking', 'installing', 'rebooting'].includes(state.apk?.state || '')
            }
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
      )}
      {state?.channel?.channel === 'beta' && (
        <Alert type="warning" message={t('settings.updates.channel.warning')} />
      )}
      {state?.channel?.rollbackAvailable && (
        <Alert
          type="warning"
          message={t('settings.updates.channel.rollbackDescription')}
          action={
            <Popconfirm
              title={t('settings.updates.channel.rollbackConfirm')}
              onConfirm={() => action('apk/rollback')}
              disabled={
                busy || ['checking', 'installing', 'rebooting'].includes(state.apk?.state || '')
              }
            >
              <Button
                disabled={
                  busy || ['checking', 'installing', 'rebooting'].includes(state.apk?.state || '')
                }
              >
                {t('settings.updates.channel.rollback')}
              </Button>
            </Popconfirm>
          }
        />
      )}
      {state && <ComponentList components={state.components} />}
      {state && <UpdateCheck notice={state.notice} onChange={refresh} />}
      {state && (
        <APKUpdates
          state={state.apk}
          known={!!state.components?.packages.some((component) => component.available)}
          busy={busy}
          action={action}
        />
      )}
      {reconnecting && <Alert type="info" message={t('settings.updates.reconnecting')} />}
    </div>
  );
};
