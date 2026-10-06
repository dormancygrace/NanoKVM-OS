import { useState } from 'react';
import { message } from 'antd';
import { useTranslation } from 'react-i18next';

import { effectiveFps, matchPreset, streamSize } from '@/lib/video-model';

import { applyVideoDraft } from '../settings/video/apply';
import { PresetPicker } from '../settings/video/presets';
import { useSyncStreamAtoms, useVideoSettings } from '../settings/video/use-video-settings';

// The toolbar's video summary: what is streamed now and one-click presets.
// Detailed choices live in Settings → Video only.
export const QuickVideo = () => {
  const { t } = useTranslation();
  const { admin, caps, browser, saved, status, refresh } = useVideoSettings(0);
  const syncAtoms = useSyncStreamAtoms();
  const [busy, setBusy] = useState(false);
  if (!caps || !browser || !saved) return null;

  const out = streamSize(saved, caps);
  const rate =
    saved.transport === 'mjpeg'
      ? `${saved.quality}%`
      : t('videoSettings.mbps', { value: saved.bitRate / 1000 });
  const summary = [
    out.width ? `${out.width} × ${out.height}` : t('videoSettings.sameAsInput'),
    t('videoSettings.fpsValue', { value: status?.measuredFps || effectiveFps(saved, caps) }),
    rate
  ].join(' · ');

  return (
    <div className="flex flex-col">
      <div className="px-3 pb-1 text-xs text-neutral-400">{summary}</div>
      {admin && (
        <>
          <div className="px-3 pt-2 text-xs tracking-wide text-neutral-500 uppercase">
            {t('videoSettings.preset.title')}
          </div>
          <PresetPicker
            compact
            caps={caps}
            browser={browser}
            current={saved}
            selected={matchPreset(saved, caps, browser)}
            disabled={busy}
            onPick={async (draft) => {
              if (busy) return;
              setBusy(true);
              try {
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
          />
        </>
      )}
    </div>
  );
};
