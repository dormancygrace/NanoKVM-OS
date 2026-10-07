import { Button } from 'antd';
import { CheckIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  buildPreset,
  PRESETS,
  type BrowserSupport,
  type PresetId,
  type VideoCapabilities,
  type VideoDraft
} from '@/lib/video-model';

type Props = {
  caps: VideoCapabilities;
  browser: BrowserSupport;
  current: VideoDraft;
  selected: PresetId | 'custom';
  disabled?: boolean;
  compact?: boolean;
  onPick: (draft: VideoDraft, id: PresetId) => void;
};

// One choice for a coherent monitor, stream and player setup; each preset
// adapts to the device and browser, and explains when it cannot apply.
export const PresetPicker = ({
  caps,
  browser,
  current,
  selected,
  disabled = false,
  compact = false,
  onPick
}: Props) => {
  const { t } = useTranslation();
  const items = PRESETS.map((id) => ({ id, built: buildPreset(id, caps, browser, current) }));

  if (compact) {
    return (
      <div className="flex flex-col" role="radiogroup" aria-label={t('videoSettings.preset.title')}>
        {items.map(({ id, built }) => {
          const unavailable = 'reason' in built;
          return (
            <Button
              key={id}
              type="text"
              role="radio"
              aria-checked={selected === id}
              disabled={disabled || unavailable}
              title={unavailable ? t(`videoSettings.reason.${built.reason}`) : undefined}
              className="flex! h-8 w-full items-center justify-start! gap-2 rounded px-3 text-left text-sm text-neutral-300 hover:bg-neutral-700/70"
              onClick={() => 'draft' in built && onPick(built.draft, id)}
            >
              <span className="w-4 text-blue-400">
                {selected === id && <CheckIcon size={16} />}
              </span>
              <span>{t(`videoSettings.preset.${id}`)}</span>
            </Button>
          );
        })}
        {selected === 'custom' && (
          <div className="flex items-center gap-2 px-3 py-1.5 text-sm text-neutral-400">
            <span className="w-4 text-blue-400">
              <CheckIcon size={16} />
            </span>
            {t('videoSettings.preset.custom')}
          </div>
        )}
      </div>
    );
  }

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="font-medium">{t('videoSettings.preset.title')}</h3>
        {selected === 'custom' && (
          <span className="text-xs text-neutral-400">{t('videoSettings.preset.custom')}</span>
        )}
      </div>
      <div
        className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3"
        role="radiogroup"
        aria-label={t('videoSettings.preset.title')}
      >
        {items.map(({ id, built }) => {
          const unavailable = 'reason' in built;
          const active = selected === id;
          return (
            <button
              key={id}
              type="button"
              role="radio"
              aria-checked={active}
              disabled={disabled || unavailable}
              onClick={() => 'draft' in built && onPick(built.draft, id)}
              className={`w-full cursor-pointer appearance-none rounded-lg border border-solid p-3 text-left text-neutral-200 transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
                active
                  ? 'border-blue-500 bg-blue-500/10'
                  : 'border-neutral-700 bg-neutral-800/40 hover:border-neutral-500'
              }`}
            >
              <span className="flex items-center justify-between gap-2 text-sm font-medium">
                {t(`videoSettings.preset.${id}`)}
                {active && <CheckIcon size={16} className="text-blue-400" />}
              </span>
              <span className="mt-1 block text-xs leading-relaxed text-neutral-400">
                {unavailable
                  ? t(`videoSettings.reason.${built.reason}`)
                  : t(`videoSettings.preset.${id}Hint`)}
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
};
