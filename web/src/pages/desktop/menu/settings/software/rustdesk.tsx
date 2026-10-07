import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, message, Popconfirm, Spin } from 'antd';
import { useAtom } from 'jotai';
import { DownloadIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getRustDeskStatus, rustDeskPackageAction, type RustDeskStatus } from '@/api/rustdesk';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll';
import { rustDeskStatusAtom } from '@/jotai/rustdesk';
import { RustDeskIcon } from '@/components/icons/rustdesk';
import { StatusBadge } from '@/components/ui/settings.tsx';

import { AddonCard } from './addon-card';
import { RustDeskVersions } from './rustdesk-versions';

export const RustDeskAddon = ({ onOpen }: { onOpen: () => void }) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.rustdesk' });
  const [status, setStatus] = useAtom(rustDeskStatusAtom);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const mounted = useRef(false);
  const working = useRef(false);
  const pending = useRef(false);
  const generation = useRef(0);
  const statusRequest = useRef<AbortController | null>(null);
  const refresh = useCallback(async () => {
    if (!mounted.current || working.current || pending.current) return;
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
      if (started === generation.current && !controller.signal.aborted) setError(t('failed'));
    } finally {
      if (started === generation.current) pending.current = false;
    }
  }, [setStatus, t]);
  useEffect(() => {
    mounted.current = true;
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 4000);
    const invalidate = () => {
      generation.current++;
      statusRequest.current?.abort();
      pending.current = false;
    };
    return () => {
      mounted.current = false;
      invalidate();
      stop();
    };
  }, [refresh]);
  const operation = async (action: 'install' | 'upgrade' | 'remove') => {
    if (!mounted.current || working.current) return;
    const entered = generation.current;
    if (!mounted.current || entered !== generation.current) return;
    const started = ++generation.current;
    statusRequest.current?.abort();
    pending.current = false;
    working.current = true;
    setBusy(true);
    try {
      const response = await rustDeskPackageAction(action);
      if (!mounted.current || started !== generation.current) return;
      if (response.code !== 0) {
        showRequestError(response, 'settings.rustdesk.failed');
        return;
      }
      message.success(t('done'));
      setError('');
    } catch (err) {
      if (mounted.current && started === generation.current)
        showRequestError(err, 'settings.rustdesk.failed');
    } finally {
      working.current = false;
      if (mounted.current) {
        setBusy(false);
        if (started === generation.current) await refresh();
      }
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
            (status.installed ? (
              <RustDeskVersions status={status} />
            ) : (
              <StatusBadge tone="neutral">{t('absent')}</StatusBadge>
            ))}
          <p className="text-fg m-0 text-sm">{t('description')}</p>
          {!status?.installed && status && !status.available && (
            <Alert type="info" title={t('unavailable')} />
          )}
          <div className="flex flex-wrap gap-2">
            {status?.installed ? (
              <>
                <Button type="primary" disabled={busy} onClick={onOpen}>
                  {t('open')}
                </Button>
                {status.update_version && (
                  <Button disabled={busy} onClick={() => void operation('upgrade')}>
                    {t('upgrade', { version: status.update_version })}
                  </Button>
                )}
                <Popconfirm title={t('deletion')} onConfirm={() => operation('remove')}>
                  <Button danger disabled={busy} icon={<Trash2Icon size={16} />}>
                    {t('remove')}
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
                {t('install')}
              </Button>
            )}
          </div>
          {status?.installed && status.source_url && (
            <a href={status.source_url} target="_blank" rel="noopener noreferrer">
              {t('source')} · AGPL-3.0
            </a>
          )}
        </>
      )}
    </AddonCard>
  );
};
