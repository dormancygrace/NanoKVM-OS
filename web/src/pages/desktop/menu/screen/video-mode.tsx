import { useEffect, useState } from 'react';
import { message, Tag, Tooltip } from 'antd';
import { useAtomValue } from 'jotai';
import { CheckIcon, TvMinimalPlayIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getEncoderCodec } from '@/lib/encoder';
import { setVideoMode as setCookie } from '@/lib/localstorage.ts';
import { isMaximumPortraitBlocked, isQhdWebRTCBlocked } from '@/lib/video-policy';
import { resolutionAtom, videoModeAtom } from '@/jotai/screen.ts';
import { MenuSubmenu } from '@/components/menu-item.tsx';

const videoModes = [
  { key: 'direct', name: 'Direct' },
  { key: 'h264', name: 'WebRTC' },
  { key: 'mjpeg', name: 'MJPEG' }
];

export const VideoMode = () => {
  const { t } = useTranslation();
  const videoMode = useAtomValue(videoModeAtom);
  const resolution = useAtomValue(resolutionAtom);
  const blocked = getEncoderCodec() === 'h265' && (resolution?.height ?? 0) > 1080;

  const [isDirectSupported, setIsDirectSupported] = useState(false);

  useEffect(() => {
    // Trust the browser secure-context decision, including loopback development.
    const isSecure = window.isSecureContext;
    const isDecoderSupported = !!window.VideoDecoder;

    setIsDirectSupported(isSecure && isDecoderSupported);
  }, []);

  async function update(mode: string) {
    if (mode === videoMode) return;

    try {
      if (await isMaximumPortraitBlocked(mode, getEncoderCodec())) {
        message.warning(t('videoSettings.portraitMaximumHint'));
        return;
      }
      if (await isQhdWebRTCBlocked(mode, getEncoderCodec())) {
        message.warning(t('videoSettings.unstableDescription'));
        return;
      }
    } catch {
      message.error(t('videoSettings.failed'));
      return;
    }
    setCookie(mode);

    // reload after changing video mode
    setTimeout(() => {
      window.location.reload();
    }, 500);
  }

  const content = (
    <>
      {!isDirectSupported && (
        <Tooltip
          title={t('screen.videoDirectTips')}
          placement="right"
          styles={{ root: { maxWidth: '270px' } }}
        >
          <div className="flex cursor-not-allowed items-center rounded py-1.5 pr-5 pl-1 text-neutral-500 select-none hover:bg-neutral-700/70">
            <div className="flex h-[14px] w-[20px] items-end text-blue-500"></div>
            <span>Direct</span>
          </div>
        </Tooltip>
      )}

      {videoModes.map(
        (mode) =>
          (isDirectSupported || mode.key !== 'direct') && (
            <div
              key={mode.key}
              className="flex cursor-pointer items-center rounded py-1.5 pr-5 pl-1 select-none hover:bg-neutral-700/70"
              aria-disabled={mode.key === 'h264' && blocked}
              onClick={() => void update(mode.key)}
            >
              <div className="flex h-[14px] w-[20px] items-end text-blue-500">
                {mode.key === videoMode && <CheckIcon size={15} />}
              </div>
              <span>{mode.name}</span>
              {mode.key === 'h264' && blocked && (
                <Tag color="gold" className="ml-2">
                  {t('videoSettings.unstableTag')}
                </Tag>
              )}
            </div>
          )
      )}
    </>
  );

  return (
    <MenuSubmenu
      title={t('screen.video')}
      content={content}
      popoverProps={{ placement: 'rightTop', arrow: false, align: { offset: [14, 0] } }}
    >
      <div className="flex h-[30px] cursor-pointer items-center space-x-2 rounded px-3 text-neutral-300 hover:bg-neutral-700/70">
        <TvMinimalPlayIcon size={18} />
        <span className="text-sm select-none">{t('screen.video')}</span>
      </div>
    </MenuSubmenu>
  );
};
