import { useAtomValue } from 'jotai';
import { CheckIcon, SquareActivityIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { updateScreen } from '@/api/vm';
import { setQuality as setCookie } from '@/lib/localstorage.ts';
import { videoModeAtom } from '@/jotai/screen.ts';
import { MenuSubmenu } from '@/components/menu-item.tsx';

import { getQualityMap } from './constants.ts';

type QualityProps = {
  quality: number;
  setQuality: (quality: number) => void;
};

export const Quality = ({ quality, setQuality }: QualityProps) => {
  const { t } = useTranslation();
  const videoMode = useAtomValue(videoModeAtom);

  const qualityList =
    videoMode === 'mjpeg'
      ? [
          { key: 1, label: t('screen.qualityLossless') },
          { key: 2, label: t('screen.qualityHigh') },
          { key: 3, label: t('screen.qualityMedium') },
          { key: 4, label: t('screen.qualityLow') }
        ]
      : Array.from(getQualityMap(videoMode)?.keys() ?? []).map((key) => ({ key, label: '' }));

  async function update(key: number) {
    const qualityMap = getQualityMap(videoMode);
    const value = qualityMap?.get(key);
    if (value === undefined) {
      return;
    }

    const rsp = await updateScreen('quality', value);
    if (rsp.code !== 0) {
      return;
    }

    setQuality(key);
    setCookie(key);
  }

  const content = (
    <>
      {qualityList.map((item) => (
        <div
          key={item.key}
          className="flex h-[30px] cursor-pointer items-center rounded pr-5 pl-1 select-none hover:bg-neutral-700/70"
          onClick={() => update(item.key)}
        >
          <div className="flex h-[14px] w-[20px] items-end text-blue-500">
            {item.key === quality && <CheckIcon size={14} />}
          </div>
          <span>
            {videoMode === 'mjpeg'
              ? item.label
              : `${(getQualityMap(videoMode)?.get(item.key) ?? 0) / 1000} Mbit/s`}
          </span>
        </div>
      ))}
    </>
  );

  return (
    <MenuSubmenu
      title={t('screen.quality')}
      content={content}
      popoverProps={{ placement: 'rightTop', arrow: false, align: { offset: [14, 0] } }}
    >
      <div className="flex h-[30px] cursor-pointer items-center space-x-2 rounded px-3 text-neutral-300 hover:bg-neutral-700/70">
        <SquareActivityIcon size={18} />
        <span className="text-sm select-none">
          {t(videoMode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate')}
        </span>
        <span className="ml-auto text-xs text-neutral-400">
          {videoMode === 'mjpeg'
            ? `${getQualityMap(videoMode)?.get(quality)}%`
            : `${(getQualityMap(videoMode)?.get(quality) ?? 0) / 1000} Mbit/s`}
        </span>
      </div>
    </MenuSubmenu>
  );
};
