import { useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button, message, Modal } from 'antd';
import { AlertCircle, AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getMemoryStatus, reboot, setVideoMemory } from '@/api/vm';
import { pickVideoMemoryMode } from '@/lib/video-memory-mode';

import { CaptureStatus, getCaptureStatusMessageKey, getVideoMemoryNeed } from './model';

export function CaptureStatusOverlay({ status }: { status: CaptureStatus | null }) {
  const { t } = useTranslation();
  const { account } = useAuth();
  const [switching, setSwitching] = useState(false);

  if (!status || status.ok) {
    return null;
  }

  const need = getVideoMemoryNeed(status);
  const mode = need?.mode.toUpperCase() ?? '';
  const text = need
    ? t('screen.captureStatus.needsVideoMemory', { size: need.size, mode })
    : t(getCaptureStatusMessageKey(status.result));
  const isWarning = status.severity === 'warning';

  const toneClass = isWarning
    ? 'border-amber-500/50 bg-neutral-900/90 text-amber-400 shadow-amber-900/20'
    : 'border-red-500/50 bg-neutral-900/90 text-red-400 shadow-red-900/20';

  const Icon = isWarning ? AlertTriangle : AlertCircle;

  // The video memory mode applies after a reboot, as in Settings -> Video.
  function switchVideoMemory() {
    if (!need) return;
    Modal.confirm({
      title: t('screen.captureStatus.switchVideoMemoryTitle', { mode }),
      content: t('videoSettings.uhdConfirm'),
      okText: t('screen.captureStatus.switchVideoMemory', { mode }),
      onOk: async () => {
        setSwitching(true);
        try {
          const memory = await getMemoryStatus();
          if (memory.code !== 0) throw new Error(memory.msg);
          const installed = pickVideoMemoryMode(memory.data.videoMemory.modes ?? [], need.mode);
          if (!installed) {
            throw new Error(t('screen.captureStatus.videoMemoryUnavailable', { mode }));
          }
          const set = await setVideoMemory(installed);
          if (set.code !== 0) throw new Error(set.msg);
          const restart = await reboot();
          if (restart.code !== 0) throw new Error(restart.msg);
          message.info(t('screen.captureStatus.videoMemoryRebooting', { mode }));
        } catch (error) {
          setSwitching(false);
          message.error(
            error instanceof Error && error.message ? error.message : t('videoSettings.failed')
          );
          throw error;
        }
      }
    });
  }

  return (
    <div
      className={`pointer-events-none absolute top-1/2 left-1/2 z-10 flex max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-wrap items-center justify-center gap-2.5 rounded-lg border px-4 py-2.5 text-sm font-medium shadow-xl backdrop-blur-md ${toneClass}`}
    >
      <Icon className="h-5 w-5 shrink-0 animate-pulse" />
      <span className={need ? 'min-w-0' : 'min-w-0 truncate'}>{text}</span>
      {need && account.role === 'admin' && (
        <Button
          size="small"
          type="primary"
          loading={switching}
          className="pointer-events-auto"
          onClick={switchVideoMemory}
        >
          {t('screen.captureStatus.switchVideoMemory', { mode })}
        </Button>
      )}
    </div>
  );
}
