import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, message, Popconfirm, Spin, Tag } from 'antd';
import { useAtom } from 'jotai';
import { DownloadIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getRustDeskStatus, rustDeskPackageAction, type RustDeskStatus } from '@/api/rustdesk';
import { pollWhileVisible } from '@/lib/visible-poll';
import { rustDeskStatusAtom } from '@/jotai/rustdesk';
import { RustDeskIcon } from '@/components/icons/rustdesk';

import { AddonCard } from './addon-card';
import { rustDeskLabels } from './rustdesk-labels';
import { RustDeskVersions } from './rustdesk-versions';

export const RustDeskAddon = ({ onOpen }: { onOpen: () => void }) => {
  const { i18n } = useTranslation();
  const l = rustDeskLabels[(i18n.resolvedLanguage || i18n.language).startsWith('ru') ? 'ru' : 'en'];
  const [status, setStatus] = useAtom(rustDeskStatusAtom);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const working = useRef(false);
  const pending = useRef(false);
  const generation = useRef(0);
  const statusRequest = useRef<AbortController | null>(null);
  const refresh = useCallback(async () => {
    if (working.current || pending.current) return;
    pending.current = true;
    const started = generation.current;
    const controller = new AbortController();
    statusRequest.current = controller;
    try {
      const response = await getRustDeskStatus(controller.signal);
      if (started !== generation.current) return;
      if (response.code !== 0) {
        setError(response.msg);
        return;
      }
      setStatus(response.data as RustDeskStatus);
      setError('');
    } catch {
      if (started === generation.current && !controller.signal.aborted) setError(l.failed);
    } finally {
      if (started === generation.current) pending.current = false;
    }
  }, [setStatus, l.failed]);
  useEffect(() => {
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 4000);
    const invalidate = () => {
      generation.current++;
      statusRequest.current?.abort();
      pending.current = false;
    };
    return () => {
      invalidate();
      stop();
    };
  }, [refresh]);
  const operation = async (action: 'install' | 'upgrade' | 'remove') => {
    if (working.current) return;
    generation.current++;
    statusRequest.current?.abort();
    pending.current = false;
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
    <AddonCard title="RustDesk" icon={<RustDeskIcon size={24} />}>
      {!status && !error ? (
        <Spin size="small" />
      ) : (
        <>
          {error && <Alert type="error" title={error} showIcon />}
          {status &&
            (status.installed ? <RustDeskVersions status={status} /> : <Tag>{l.absent}</Tag>)}
          <p className="text-sm text-neutral-300">{l.description}</p>
          {!status?.installed && status && !status.available && (
            <Alert type="info" title={l.unavailable} />
          )}
          <div className="flex flex-wrap gap-2">
            {status?.installed ? (
              <>
                <Button type="primary" disabled={busy} onClick={onOpen}>
                  {l.open}
                </Button>
                {status.update_version && (
                  <Button disabled={busy} onClick={() => void operation('upgrade')}>
                    {l.upgrade.replace('{version}', status.update_version)}
                  </Button>
                )}
                <Popconfirm title={l.deletion} onConfirm={() => operation('remove')}>
                  <Button danger disabled={busy} icon={<Trash2Icon size={16} />}>
                    {l.remove}
                  </Button>
                </Popconfirm>
              </>
            ) : (
              <Button
                type="primary"
                loading={busy}
                icon={<DownloadIcon size={16} />}
                disabled={!status?.available}
                onClick={() => void operation('install')}
              >
                {l.install}
              </Button>
            )}
          </div>
          {status?.installed && status.source_url && (
            <a href={status.source_url} target="_blank" rel="noopener noreferrer">
              {l.source} · AGPL-3.0
            </a>
          )}
        </>
      )}
    </AddonCard>
  );
};
