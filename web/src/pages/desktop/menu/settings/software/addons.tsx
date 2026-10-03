import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Modal, Progress, Spin } from 'antd';
import { useAtom } from 'jotai';
import { DownloadIcon, ExternalLinkIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getRuntimeStatus, installRuntime, uninstallRuntime } from '@/api/picoclaw.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { picoclawRuntimeStatusAtom } from '@/jotai/picoclaw.ts';
import { Robot } from '@/components/icons/robot.tsx';

import { AddonCard } from './addon-card';
import { RustDeskAddon } from './rustdesk';

export const Addons = ({
  onOpen,
  onOpenRustDesk
}: {
  onOpen: () => void;
  onOpenRustDesk: () => void;
}) => {
  const { t } = useTranslation();
  const [status, setStatus] = useAtom(picoclawRuntimeStatusAtom);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const pending = useRef(false);
  const generation = useRef(0);
  const [modal, contextHolder] = Modal.useModal();
  const refresh = useCallback(async () => {
    if (pending.current) return;
    pending.current = true;
    const started = generation.current;
    try {
      const response = await getRuntimeStatus();
      if (started !== generation.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      setStatus(response.data);
    } catch (err) {
      if (started === generation.current)
        setError(err instanceof Error ? err.message : t('settings.software.requestFailed'));
    } finally {
      pending.current = false;
    }
  }, [setStatus, t]);
  useEffect(() => {
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 2000);
    const invalidate = () => {
      generation.current++;
    };
    return () => {
      invalidate();
      stop();
    };
  }, [refresh]);

  const run = async (remove: boolean) => {
    if (busy || status?.installing) return;
    setBusy(true);
    setError('');
    generation.current++;
    try {
      const response = await (remove ? uninstallRuntime() : installRuntime());
      if (response.code !== 0) throw new Error(response.msg);
      if (response.data?.status) setStatus(response.data.status);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : t('settings.software.requestFailed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6 py-6">
      {contextHolder}
      <h2 className="text-lg font-semibold">{t('settings.software.addons.title')}</h2>
      <AddonCard title="PicoClaw" icon={<Robot size={24} />}>
        <p className="text-sm text-neutral-300">
          {t('settings.software.addons.picoclawDescription')}
        </p>
        <a
          className="inline-flex items-center gap-1 text-sm text-emerald-400 no-underline!"
          href="https://github.com/sipeed/picoclaw/releases/latest"
          target="_blank"
          rel="noreferrer"
        >
          {t('settings.software.addons.source')} <ExternalLinkIcon size={14} />
        </a>
        {(error || status?.last_error) && (
          <Alert type="error" title={error || status?.last_error} showIcon />
        )}
        {!status ? (
          <Spin size="small" />
        ) : status.installing ? (
          <div>
            <div className="text-sm text-neutral-300">
              {t(`picoclaw.install.stages.${status.install_stage || 'preparing'}`)}
            </div>
            <Progress percent={status.install_progress || 0} status="active" />
          </div>
        ) : status.installed ? (
          <div className="flex flex-wrap gap-2">
            <Button type="primary" disabled={busy} onClick={onOpen}>
              {t('settings.software.addons.open')}
            </Button>
            <Button
              danger
              loading={busy}
              icon={<Trash2Icon size={16} />}
              onClick={() =>
                modal.confirm({
                  title: t('picoclaw.uninstall.confirmTitle'),
                  content: t('picoclaw.uninstall.confirmContent'),
                  okText: t('picoclaw.uninstall.confirmOk'),
                  cancelText: t('picoclaw.uninstall.confirmCancel'),
                  onOk: () => run(true)
                })
              }
            >
              {t('picoclaw.uninstall.menuLabel')}
            </Button>
          </div>
        ) : (
          <Button
            type="primary"
            loading={busy}
            icon={<DownloadIcon size={16} />}
            onClick={() => void run(false)}
          >
            {t('picoclaw.install.install')}
          </Button>
        )}
      </AddonCard>
      <RustDeskAddon onOpen={onOpenRustDesk} />
    </div>
  );
};
