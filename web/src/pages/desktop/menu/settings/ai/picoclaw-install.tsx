import { useEffect, useState } from 'react';
import { Alert, Button } from 'antd';
import { DownloadIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getRuntimeStatus, installRuntime } from '@/api/picoclaw.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { type PicoclawRuntimeStatus } from '@/jotai/picoclaw.ts';

// Settings > AI > PicoClaw without PicoClaw: install it here (the latest
// release, as Software > Add-ons does) and follow the download. The status
// then reports installed and the page shows its settings.
export const PicoclawInstall = ({
  status,
  onStatus
}: {
  status: PicoclawRuntimeStatus;
  onStatus: (status: PicoclawRuntimeStatus) => void;
}) => {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!status.installing) return;
    return pollWhileVisible(() => {
      getRuntimeStatus()
        .then((response) => {
          if (response.code === 0) onStatus(response.data as PicoclawRuntimeStatus);
        })
        .catch(() => {});
    }, 2000);
  }, [status.installing, onStatus]);

  async function install() {
    if (busy || status.installing) return;
    setBusy(true);
    try {
      const response = await installRuntime();
      if (response.code !== 0) throw new Error(response.msg);
      setError('');
      if (response.data?.status) onStatus(response.data.status as PicoclawRuntimeStatus);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('settings.software.requestFailed'));
    } finally {
      setBusy(false);
    }
  }

  const failure = error || status.last_error;
  return (
    <div className="space-y-3">
      <Alert
        type="info"
        showIcon
        message={
          status.installing
            ? t('picoclaw.install.installing')
            : t('settings.ai.picoclaw.notInstalled')
        }
        description={
          // A line, not antd's Progress: Progress in this lazy chunk makes the
          // antd chunks import each other.
          status.installing ? (
            <span className="text-fg text-sm">
              {t(`picoclaw.install.stages.${status.install_stage || 'preparing'}`)} ·{' '}
              {status.install_progress || 0}%
            </span>
          ) : undefined
        }
        action={
          status.installing ? undefined : (
            <Button
              size="small"
              type="primary"
              icon={<DownloadIcon size={14} />}
              loading={busy}
              onClick={() => void install()}
            >
              {t('picoclaw.install.install')}
            </Button>
          )
        }
      />
      {failure && <Alert type="error" showIcon message={failure} />}
    </div>
  );
};
