import { useEffect } from 'react';
import { useAuth } from '@/contexts/auth';
import { useAtom, useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getScreen } from '@/api/vm';
import {
  resolutionAtom,
  streamFpsAtom,
  streamGopAtom,
  streamQualityAtom,
  videoModeAtom
} from '@/jotai/screen';

import { getQualityMap } from './constants';
import { Fps } from './fps';
import { Gop } from './gop';
import { Quality } from './quality';
import { Resolution } from './resolution';

export const StreamControls = ({ advanced = false }: { advanced?: boolean }) => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const mode = useAtomValue(videoModeAtom);
  const [fps, setFps] = useAtom(streamFpsAtom);
  const [quality, setQuality] = useAtom(streamQualityAtom);
  const [gop, setGop] = useAtom(streamGopAtom);
  const [resolution, setResolution] = useAtom(resolutionAtom);
  useEffect(() => {
    let active = true;
    void getScreen()
      .then((rsp) => {
        if (!active || rsp.code !== 0) return;
        setFps(rsp.data.fps);
        setGop(rsp.data.gop);
        setResolution({ width: rsp.data.width, height: rsp.data.height });
        const value = mode === 'mjpeg' ? rsp.data.quality : rsp.data.bitRate;
        const key = [...(getQualityMap(mode) ?? [])].find(([, v]) => v === value)?.[0];
        if (key !== undefined) setQuality(key);
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [mode, setFps, setGop, setQuality, setResolution]);

  if (!admin) {
    const qualityMap = getQualityMap(mode);
    const qualityValue = qualityMap?.get(quality);
    const resolutionValue = resolution;
    const resolutionLabel = resolutionValue?.height
      ? `${resolutionValue.width} × ${resolutionValue.height}`
      : t('videoSettings.sameAsInput');
    const qualityLabel =
      qualityValue === undefined
        ? '—'
        : mode === 'mjpeg'
          ? `${qualityValue}%`
          : `${qualityValue / 1000} Mbit/s`;
    const summaryRow = (label: string, value: string) => (
      <div className="flex items-center justify-between gap-4 px-3 py-1 text-sm">
        <span className="text-neutral-400">{label}</span>
        <span className="whitespace-nowrap text-neutral-300">{value}</span>
      </div>
    );

    return (
      <div className="space-y-0.5 py-1" aria-label={t('videoSettings.stream')}>
        {advanced ? (
          summaryRow('GOP', `${gop}`)
        ) : (
          <>
            {summaryRow(t('videoSettings.streamResolution'), resolutionLabel)}
            {summaryRow(t('screen.fps'), `${fps} FPS`)}
            {summaryRow(
              t(mode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
              qualityLabel
            )}
          </>
        )}
      </div>
    );
  }

  return advanced ? (
    <Gop gop={gop} setGop={setGop} />
  ) : (
    <>
      <Resolution />
      <Fps fps={fps} setFps={setFps} maxFps={120} />
      <Quality quality={quality} setQuality={setQuality} />
    </>
  );
};
