import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, message, Spin } from 'antd';
import { DownloadIcon, ShieldCheckIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/acme.ts';
import type { AcmeStatus } from '@/lib/acme.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { StatusBadge } from '@/components/ui/settings.tsx';

import { AddonCard } from './addon-card';

export const AcmeAddon = ({
  onOpen,
  onInstalled
}: {
  onOpen: () => void;
  // The add-on list sorts the card into Installed or Available.
  onInstalled?: (installed: boolean) => void;
}) => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<AcmeStatus>();
  useEffect(() => {
    if (status) onInstalled?.(status.installed);
  }, [status, onInstalled]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const mounted = useRef(false);
  const working = useRef(false);
  const pending = useRef(false);
  const generation = useRef(0);

  const refresh = useCallback(async () => {
    if (!mounted.current || pending.current || working.current) return;
    pending.current = true;
    const started = generation.current;
    try {
      const response = await api.getStatus();
      if (!mounted.current || started !== generation.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      setStatus(response.data);
      setError('');
    } catch {
      if (mounted.current && started === generation.current)
        setError(t('settings.network.acme.failed'));
    } finally {
      pending.current = false;
    }
  }, [t]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 4000);
    const invalidate = () => {
      generation.current++;
    };
    return () => {
      mounted.current = false;
      invalidate();
      stop();
    };
  }, [refresh]);

  const install = async () => {
    if (working.current || !status || status.installed || status.running) return;
    working.current = true;
    setBusy(true);
    const started = ++generation.current;
    try {
      const confirmed = await confirmAction({
        title: t('settings.network.acme.installTitle'),
        content: t('settings.network.acme.installConfirm')
      });
      if (!confirmed || !mounted.current || started !== generation.current) return;
      const response = await api.install();
      if (!mounted.current || started !== generation.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      setStatus(response.data as AcmeStatus);
      setError('');
      void message.success(t('settings.network.acme.installed'));
    } catch (err) {
      if (mounted.current && started === generation.current)
        setError(err instanceof Error ? err.message : t('settings.network.acme.failed'));
    } finally {
      working.current = false;
      if (mounted.current) {
        setBusy(false);
        void refresh();
      }
    }
  };

  return (
    <AddonCard title="acme.sh" icon={<ShieldCheckIcon size={24} />}>
      <p className="text-fg m-0 text-sm">{t('settings.network.acme.description')}</p>
      {error && <Alert type="error" title={error} showIcon />}
      {!status ? (
        !error && <Spin size="small" />
      ) : (
        <>
          <StatusBadge tone={status.installed ? 'success' : 'neutral'}>
            {status.installed
              ? 'acme.sh ' + (status.version ?? '')
              : t('settings.network.acme.notInstalled')}
          </StatusBadge>
          <div className="flex flex-wrap gap-2">
            {!status.installed && (
              <Button
                type="primary"
                icon={<DownloadIcon size={16} />}
                loading={busy}
                disabled={status.running}
                onClick={() => void install()}
              >
                {t('settings.software.addons.install')}
              </Button>
            )}
            <Button
              type={status.installed ? 'primary' : 'default'}
              disabled={busy}
              onClick={onOpen}
            >
              {t('settings.network.acme.title')}
            </Button>
          </div>
          {!status.installed && (
            <p className="text-fg-muted m-0 text-xs">{t('settings.network.acme.installHint')}</p>
          )}
        </>
      )}
    </AddonCard>
  );
};
