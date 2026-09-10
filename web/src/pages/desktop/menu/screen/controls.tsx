import { useEffect } from 'react';
import { useAtom, useAtomValue } from 'jotai';

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
  const mode = useAtomValue(videoModeAtom);
  const [fps, setFps] = useAtom(streamFpsAtom);
  const [quality, setQuality] = useAtom(streamQualityAtom);
  const [gop, setGop] = useAtom(streamGopAtom);
  const [, setResolution] = useAtom(resolutionAtom);
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
  return advanced ? (
    <Gop gop={gop} setGop={setGop} />
  ) : (
    <>
      <Resolution />
      <Fps fps={fps} setFps={setFps} maxFps={60} />
      <Quality quality={quality} setQuality={setQuality} />
    </>
  );
};
