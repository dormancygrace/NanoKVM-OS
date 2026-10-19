import { useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button, message, Popconfirm } from 'antd';
import { AlertCircle, AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { reboot } from '@/api/vm';

import { CaptureStatus, getCaptureStatusMessageKey, getVideoMemoryNeed } from './model';

export function CaptureStatusOverlay({ status }: { status: CaptureStatus | null }) {
  const { t } = useTranslation();
  const { account } = useAuth();
  const [restarting, setRestarting] = useState(false);

  if (!status || status.ok) {
    return null;
  }

  const need = getVideoMemoryNeed(status);
  const text = need
    ? t('screen.captureStatus.needsVideoMemory', { size: need.size })
    : t(getCaptureStatusMessageKey(status.result));
  const isWarning = status.severity === 'warning';

  const toneClass = isWarning
    ? 'border-amber-500/50 bg-neutral-900/90 text-amber-400 shadow-amber-900/20'
    : 'border-red-500/50 bg-neutral-900/90 text-red-400 shadow-red-900/20';

  const Icon = isWarning ? AlertTriangle : AlertCircle;

  // The booted video memory is the one of the kernel before the update; a
  // restart boots the installed kernel with the shared video memory.
  async function restart() {
    setRestarting(true);
    try {
      const rsp = await reboot();
      if (rsp.code !== 0) throw new Error(rsp.msg);
    } catch (error) {
      setRestarting(false);
      message.error(
        error instanceof Error && error.message ? error.message : t('videoSettings.failed')
      );
    }
  }

  return (
    <div
      className={`pointer-events-none absolute top-1/2 left-1/2 z-10 flex max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-wrap items-center justify-center gap-2.5 rounded-lg border px-4 py-2.5 text-sm font-medium shadow-xl backdrop-blur-md ${toneClass}`}
    >
      <Icon className="h-5 w-5 shrink-0 animate-pulse" />
      <span className={need ? 'min-w-0' : 'min-w-0 truncate'}>{text}</span>
      {need && account.role === 'admin' && (
        <Popconfirm
          title={t('screen.captureStatus.restartConfirm')}
          onConfirm={() => void restart()}
        >
          <Button size="small" type="primary" loading={restarting} className="pointer-events-auto">
            {t('screen.captureStatus.restart')}
          </Button>
        </Popconfirm>
      )}
    </div>
  );
}
