import { useState } from 'react';
import { Button, message } from 'antd';
import { CheckIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  draftAutoBitrate,
  modeOf,
  recommendedMode,
  videoModes,
  withMode
} from '@/lib/video-model';

import { applyVideoDraft } from '../settings/video/apply';
import { useSyncStreamAtoms, useVideoSettings } from '../settings/video/use-video-settings';

// The toolbar's video summary: what is streamed now, measured, and a quick
// choice of the video mode. The bitrate and everything else live in
// Settings → Video.
export const QuickVideo = () => {
  const { t } = useTranslation();
  const { admin, caps, saved, status, refresh } = useVideoSettings(3000);
  const syncAtoms = useSyncStreamAtoms();
  const [busy, setBusy] = useState(false);
  if (!caps || !saved || !status) return null;

  const mjpeg = saved.transport === 'mjpeg';
  const width = (mjpeg ? status.mjpegOutputWidth : status.videoOutputWidth) || status.outputWidth;
  const height =
    (mjpeg ? status.mjpegOutputHeight : status.videoOutputHeight) || status.outputHeight;
  const summary = [
    width && height ? `${width} × ${height}` : t('videoSettings.sameAsInput'),
    t('videoSettings.fpsValue', { value: status.measuredFps || 0 }),
    mjpeg
      ? `${saved.quality}%`
      : t('videoSettings.measuredBitrate', {
          value: ((status.measuredKbps || 0) / 1000).toFixed(1)
        })
  ].join(' · ');

  const current = modeOf(saved, caps);
  const recommended = recommendedMode(caps);
  // A short list: landscape modes from 60 fps up, and the current one. The
  // others, portrait included, are in Settings → Video.
  const modes = videoModes(caps).filter(
    (m) => m.key === current?.key || (m.available && !m.portrait && m.fps >= 60)
  );
  const autoBitrate = saved.bitRate === draftAutoBitrate(saved, caps);

  return (
    <div className="flex flex-col">
      <div className="px-3 pb-1 text-xs text-neutral-400">{summary}</div>
      {/* A Cube receiver applies a monitor change only after a power cycle,
          which Settings → Video confirms first. */}
      {admin && !caps.monitor.requiresPowerCycle && (
        <>
          <div className="px-3 pt-2 text-xs tracking-wide text-neutral-500 uppercase">
            {t('videoSettings.mode')}
          </div>
          <div className="flex flex-col" role="radiogroup" aria-label={t('videoSettings.mode')}>
            {modes.map((mode) => (
              <Button
                key={mode.key}
                type="text"
                role="radio"
                aria-checked={mode.key === current?.key}
                disabled={busy}
                className="text-fg hover:bg-surface-raised flex! h-8 w-full items-center justify-start! gap-2 rounded px-3 text-left text-sm"
                onClick={async () => {
                  if (busy || mode.key === current?.key) return;
                  setBusy(true);
                  try {
                    const draft = withMode(saved, mode, caps);
                    // An automatic bitrate follows the mode.
                    if (autoBitrate) draft.bitRate = draftAutoBitrate(draft, caps);
                    const result = await applyVideoDraft(saved, draft, caps, {
                      admin,
                      confirmPowerCycle: false
                    });
                    syncAtoms(draft, caps);
                    if (!result.reloading) message.success(t('videoSettings.applied'));
                  } catch (error) {
                    message.error(
                      error instanceof Error && error.message !== 'video-settings-failed'
                        ? error.message
                        : t('videoSettings.failed')
                    );
                  } finally {
                    await refresh();
                    setBusy(false);
                  }
                }}
              >
                <span className="text-info w-4">
                  {mode.key === current?.key && <CheckIcon size={16} />}
                </span>
                <span>
                  {`${mode.width} × ${mode.height} · ${t('videoSettings.fpsValue', { value: mode.fps })}`}
                  {mode.key === recommended?.key && (
                    <span className="text-fg-muted"> · {t('videoSettings.modeRecommended')}</span>
                  )}
                </span>
              </Button>
            ))}
          </div>
        </>
      )}
    </div>
  );
};
