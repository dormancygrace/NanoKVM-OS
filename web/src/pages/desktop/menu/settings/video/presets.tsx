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
              className="text-fg hover:bg-surface-raised flex! h-8 w-full items-center justify-start! gap-2 rounded px-3 text-left text-sm"
              onClick={() => 'draft' in built && onPick(built.draft, id)}
            >
              <span className="text-info w-4">{selected === id && <CheckIcon size={16} />}</span>
              <span>{t(`videoSettings.preset.${id}`)}</span>
            </Button>
          );
        })}
        {selected === 'custom' && (
          <div className="text-fg-muted flex items-center gap-2 px-3 py-1.5 text-sm">
            <span className="text-info w-4">
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
        <h3 className="m-0 text-sm font-medium">{t('videoSettings.preset.title')}</h3>
        {selected === 'custom' && (
          <span className="text-fg-muted text-xs">{t('videoSettings.preset.custom')}</span>
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
              className={`text-fg w-full cursor-pointer appearance-none rounded-lg border border-solid p-3 text-left transition-colors disabled:cursor-not-allowed disabled:opacity-40 ${
                active ? 'border-info bg-info/10' : 'border-line bg-surface hover:border-fg-muted'
              }`}
            >
              <span className="flex items-center justify-between gap-2 text-sm font-medium">
                {t(`videoSettings.preset.${id}`)}
                {active && <CheckIcon size={16} className="text-info" />}
              </span>
              <span className="text-fg-muted mt-1 block text-xs leading-relaxed">
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
