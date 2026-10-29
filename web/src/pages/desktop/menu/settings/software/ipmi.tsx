import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Spin } from 'antd';
import { DownloadIcon, ServerIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { type CatalogAddon } from '@/api/addons.ts';
import { getBmcStatus, ipmiPackageAction, type BmcStatus } from '@/api/bmc.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { StatusBadge } from '@/components/ui/settings.tsx';

import { AddonCard } from './addon-card';
import { MinOsNotice } from './catalog-addon';

// The IPMI add-on's card in Software > Add-ons; its settings are on the
// System > Redfish and IPMI page. entry is the add-on in the repository list.
export const IpmiAddon = ({
  onOpen,
  entry,
  os
}: {
  onOpen: () => void;
  entry?: CatalogAddon;
  os?: string;
}) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.bmc' });
  const [status, setStatus] = useState<BmcStatus>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const mounted = useRef(false);
  const working = useRef(false);

  const refresh = useCallback(async () => {
    if (!mounted.current || working.current) return;
    try {
      const response = await getBmcStatus();
      if (!mounted.current || working.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      setStatus(response.data);
      setError('');
    } catch {
      if (mounted.current) setError(t('failed'));
    }
  }, [t]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 5000);
    return () => {
      mounted.current = false;
      stop();
    };
  }, [refresh]);

  const run = async (action: 'install' | 'uninstall') => {
    if (working.current) return;
    if (action === 'uninstall') {
      const confirmed = await confirmAction({
        title: t('removeTitle'),
        content: t('removeConfirm'),
        danger: true
      });
      if (!confirmed) return;
    }
    working.current = true;
    setBusy(true);
    try {
      const response = await ipmiPackageAction(action);
      if (response.code !== 0) throw new Error(response.msg);
      setError('');
    } catch (err) {
      if (mounted.current) setError(err instanceof Error ? err.message : t('failed'));
    } finally {
      working.current = false;
      if (mounted.current) {
        setBusy(false);
        void refresh();
      }
    }
  };

  return (
    <AddonCard title={t('ipmi')} icon={<ServerIcon size={24} />}>
      <p className="text-fg m-0 text-sm">{t('ipmiAddonDescription')}</p>
      {error && <Alert type="error" title={error} showIcon />}
      {!status ? (
        !error && <Spin size="small" />
      ) : status.ipmiInstalled ? (
        <>
          <StatusBadge tone={status.ipmiRunning ? 'success' : 'neutral'}>
            {status.ipmiRunning ? t('running') : t('stopped')}
          </StatusBadge>
          <div className="flex flex-wrap gap-2">
            <Button type="primary" disabled={busy} onClick={onOpen}>
              {t('open')}
            </Button>
            <Button
              danger
              loading={busy}
              icon={<Trash2Icon size={16} />}
              onClick={() => void run('uninstall')}
            >
              {t('remove')}
            </Button>
          </div>
        </>
      ) : (
        <>
          <MinOsNotice addon={entry} os={os} />
          <div className="flex flex-wrap gap-2">
            <Button
              type="primary"
              loading={busy}
              disabled={entry && !entry.compatible}
              icon={<DownloadIcon size={16} />}
              onClick={() => void run('install')}
            >
              {t('install')}
            </Button>
          </div>
        </>
      )}
    </AddonCard>
  );
};
