import { useEffect, useState } from 'react';
import { Tag } from 'antd';
import { useAtomValue } from 'jotai';
import { CheckIcon, ClapperboardIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  getEncoderCodec,
  isEncoderCodecSupported,
  setEncoderCodec,
  type EncoderCodec,
  type EncoderTransport
} from '@/lib/encoder.ts';
import { videoModeAtom } from '@/jotai/screen.ts';
import { MenuSubmenu } from '@/components/menu-item.tsx';

const codecs: Array<{ key: EncoderCodec; name: string }> = [
  { key: 'h265', name: 'H.265 / HEVC' },
  { key: 'h264', name: 'H.264 / AVC' }
];

export const Codec = () => {
  const { t } = useTranslation();
  const videoMode = useAtomValue(videoModeAtom);
  const [codec, setCodec] = useState(getEncoderCodec);
  const [h265Supported, setH265Supported] = useState(false);
  const [capabilityReady, setCapabilityReady] = useState(false);

  useEffect(() => {
    let disposed = false;
    const transport: EncoderTransport = videoMode === 'h264' ? 'webrtc' : 'direct';

    void isEncoderCodecSupported(transport, 'h265').then((supported) => {
      if (disposed) return;
      setH265Supported(supported);
      setCapabilityReady(true);

      setCodec(getEncoderCodec());
    });

    return () => {
      disposed = true;
    };
  }, [videoMode]);

  function update(nextCodec: EncoderCodec) {
    if (nextCodec === 'h265' && !h265Supported) return;

    setEncoderCodec(nextCodec);
    if (nextCodec === codec) return;
    setCodec(nextCodec);
    window.setTimeout(() => window.location.reload(), 250);
  }

  const content = (
    <>
      {codecs.map((item) => {
        const disabled = item.key === 'h265' && (!capabilityReady || !h265Supported);
        return (
          <div
            key={item.key}
            className={`flex select-none items-center rounded py-1.5 pl-1 pr-5 ${
              disabled
                ? 'cursor-not-allowed text-neutral-500'
                : 'cursor-pointer hover:bg-neutral-700/70'
            }`}
            onClick={() => !disabled && update(item.key)}
          >
            <div className="flex h-[14px] w-[20px] items-end text-blue-500">
              {item.key === codec && <CheckIcon size={15} />}
            </div>
            <span>
              {item.name}
              {item.key === 'h265' && videoMode === 'h264' && (
                <Tag color="gold" className="ml-2">
                  {t('videoSettings.unstableTag')}
                </Tag>
              )}
              {disabled && capabilityReady ? ` (${t('screen.unsupported')})` : ''}
            </span>
          </div>
        );
      })}
    </>
  );

  return (
    <MenuSubmenu
      title={t('screen.codec')}
      content={content}
      popoverProps={{ placement: 'rightTop', arrow: false, align: { offset: [14, 0] } }}
    >
      <div className="flex h-[30px] cursor-pointer items-center space-x-2 rounded px-3 text-neutral-300 hover:bg-neutral-700/70">
        <ClapperboardIcon size={18} />
        <span className="select-none text-sm">{t('screen.codec')}</span>
      </div>
    </MenuSubmenu>
  );
};
