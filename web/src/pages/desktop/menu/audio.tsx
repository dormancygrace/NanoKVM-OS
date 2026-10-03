import { Button, Slider, Switch } from 'antd';
import { Volume2Icon, VolumeXIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import type { useUsbAudio } from '@/hooks/useUsbAudio.ts';
import { MenuItem } from '@/components/menu-item.tsx';

export const AudioMenu = ({ audio }: { audio: ReturnType<typeof useUsbAudio> }) => {
  const { t } = useTranslation();
  const {
    available,
    playing,
    wanted,
    blocked,
    busy,
    error,
    receiving,
    volume,
    setVolume,
    playback,
    start,
    stop
  } = audio;
  if (!available) return null;
  return (
    <MenuItem
      title={t('audio.title')}
      icon={
        playing && volume > 0 ? (
          <Volume2Icon size={22} className="text-emerald-400" />
        ) : (
          <VolumeXIcon size={22} />
        )
      }
      content={
        <div className="w-56">
          <div className="flex items-center justify-between gap-4">
            <span>{t('audio.listen')}</span>
            <Switch
              checked={wanted}
              loading={busy}
              onChange={(on) => (on ? void start() : stop())}
              aria-label={t('audio.listen')}
            />
          </div>
          {blocked && (
            <Button className="mt-2" onClick={() => void start()}>
              {t('audio.resume', { defaultValue: 'Resume audio' })}
            </Button>
          )}
          <div className="mt-3">
            {t('audio.volume')}: {volume}%
          </div>
          <Slider
            min={0}
            max={100}
            value={volume}
            aria-label={t('audio.volume')}
            onChange={(value) => {
              setVolume(value);
              const active = playback.current;
              if (active)
                active.gain.gain.setTargetAtTime(value / 100, active.context.currentTime, 0.01);
            }}
          />
          {playing && (
            <div role="status" className="text-xs text-neutral-400">
              {t(receiving ? 'audio.receiving' : 'audio.waiting')}
            </div>
          )}
          {error && (
            <div role="alert" className="text-xs text-amber-400">
              {t(`audio.errors.${error}`, { defaultValue: t('audio.failed') })}
            </div>
          )}
        </div>
      }
    />
  );
};
