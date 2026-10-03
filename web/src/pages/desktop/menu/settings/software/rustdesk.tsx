import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Card, message, Popconfirm, Space, Tag } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getRustDeskStatus, rustDeskPackageAction, type RustDeskStatus } from '@/api/rustdesk';
import { getBaseUrl } from '@/lib/service';
import { pollWhileVisible } from '@/lib/visible-poll';
import { rustDeskStatusAtom } from '@/jotai/rustdesk';

import { rustDeskLabels } from './rustdesk-labels';

export const RustDeskAddon = ({ onOpen }: { onOpen: () => void }) => {
  const { i18n } = useTranslation();
  const l = rustDeskLabels[(i18n.resolvedLanguage || i18n.language).startsWith('ru') ? 'ru' : 'en'];
  const [status, setStatus] = useAtom(rustDeskStatusAtom);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const working = useRef(false);
  const pending = useRef(false);
  const refresh = useCallback(async () => {
    if (working.current || pending.current) return;
    pending.current = true;
    try {
      const response = await getRustDeskStatus();
      if (response.code !== 0) {
        setError(response.msg);
        return;
      }
      setStatus(response.data as RustDeskStatus);
      setError('');
    } catch {
      setError(l.failed);
    } finally {
      pending.current = false;
    }
  }, [setStatus, l.failed]);
  useEffect(() => {
    void refresh();
    return pollWhileVisible(() => void refresh(), 4000);
  }, [refresh]);
  const operation = async (action: 'install' | 'upgrade' | 'remove') => {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    try {
      const response = await rustDeskPackageAction(action);
      if (response.code !== 0) {
        setError(response.msg);
        return;
      }
      message.success(l.done);
      setError('');
    } catch {
      setError(l.failed);
    } finally {
      working.current = false;
      setBusy(false);
      await refresh();
    }
  };
  return (
    <Card title="RustDesk" loading={!status && !error} className="min-w-0">
      <Space direction="vertical" size="middle" className="w-full">
        {error && <Alert type="error" title={error} showIcon />}
        <Tag>
          {status?.installed ? l.installed : l.absent} {status?.version}
        </Tag>
        <p>{l.explain}</p>
        {!status?.installed && status && !status.available && (
          <Alert type="info" title={l.unavailable} />
        )}
        <div className="flex flex-wrap gap-2">
          {status?.installed ? (
            <>
              <Button type="primary" disabled={busy} onClick={onOpen}>
                {l.open}
              </Button>
              <Button disabled={busy} onClick={() => void operation('upgrade')}>
                {l.upgrade}
              </Button>
              <Popconfirm title={l.deletion} onConfirm={() => operation('remove')}>
                <Button danger disabled={busy}>
                  {l.remove}
                </Button>
              </Popconfirm>
            </>
          ) : (
            <Button
              type="primary"
              loading={busy}
              disabled={!status?.available}
              onClick={() => void operation('install')}
            >
              {l.install}
            </Button>
          )}
        </div>
        {status?.installed && (
          <a href={getBaseUrl('http') + '/api/addons/rustdesk/source'}>{l.source} · AGPL-3.0</a>
        )}
      </Space>
    </Card>
  );
};
