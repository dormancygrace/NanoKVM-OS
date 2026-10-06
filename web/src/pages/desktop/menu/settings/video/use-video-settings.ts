import { useCallback, useEffect, useMemo, useState } from 'react';
import { useAuth } from '@/contexts/auth';
import { useAtomValue, useSetAtom } from 'jotai';

import { getScreen, getVideoCapabilities } from '@/api/vm';
import { getEncoderCodec, isEncoderCodecSupported } from '@/lib/encoder';
import * as storage from '@/lib/localstorage';
import type { BrowserSupport, Transport, VideoCapabilities, VideoDraft } from '@/lib/video-model';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import {
  resolutionAtom,
  streamFpsAtom,
  streamGopAtom,
  streamQualityAtom,
  videoModeAtom
} from '@/jotai/screen';

import { getQualityMap } from '../../screen/constants';
import { storedMode } from './apply';

// Shared settings and status returned by GET /api/vm/screen.
export type ScreenStatus = {
  width: number;
  height: number;
  fps: number;
  quality: number;
  bitRate: number;
  gop: number;
  gopMode: number;
  gopModeActive: number;
  gopModeRestartRequired: boolean;
  mjpegChroma: number;
  mjpegChromaActive: number;
  mjpegChromaFallback: string;
  monitor: number;
  portrait: boolean;
  portraitResolution: number;
  inputWidth: number;
  inputHeight: number;
  outputWidth: number;
  outputHeight: number;
  mjpegOutputWidth: number;
  mjpegOutputHeight: number;
  videoOutputWidth: number;
  videoOutputHeight: number;
  effectiveFps: number;
  measuredFps: number;
};

const transportOf = (mode: string): Transport =>
  mode === 'h264' ? 'webrtc' : mode === 'mjpeg' ? 'mjpeg' : 'direct';

export function savedDraft(status: ScreenStatus, mode: string): VideoDraft {
  return {
    monitor: status.monitor,
    portrait: status.portrait ? status.portraitResolution : 0,
    height: status.height,
    fps: status.fps,
    transport: transportOf(mode),
    codec: getEncoderCodec(),
    bitRate: status.bitRate,
    quality: status.quality,
    gop: status.gop,
    gopMode: status.gopMode,
    mjpegChroma: status.mjpegChroma === 422 ? 422 : 420,
    directPlayback: storage.getDirectPlayback(),
    frameDetect: storage.getFrameDetect()
  };
}

let browserSupport: Promise<BrowserSupport> | undefined;

// What this browser plays; probed once per page.
export function probeBrowserSupport() {
  return (browserSupport ??= (async () => ({
    direct: window.isSecureContext && !!window.VideoDecoder,
    webrtc: !!window.RTCPeerConnection,
    directH265: await isEncoderCodecSupported('direct', 'h265'),
    webrtcH265: await isEncoderCodecSupported('webrtc', 'h265')
  }))());
}

// Device status, capabilities and the saved draft, refreshed while visible.
export function useVideoSettings(pollMs = 3000) {
  const { account } = useAuth();
  const mode = useAtomValue(videoModeAtom);
  const [status, setStatus] = useState<ScreenStatus>();
  const [caps, setCaps] = useState<VideoCapabilities>();
  const [browser, setBrowser] = useState<BrowserSupport>();
  const [failed, setFailed] = useState(false);

  const refresh = useCallback(async () => {
    try {
      const [screen, capabilities] = await Promise.all([getScreen(), getVideoCapabilities()]);
      if (screen.code !== 0) throw new Error(screen.msg);
      if (capabilities.code !== 0) throw new Error(capabilities.msg);
      setStatus(screen.data);
      setCaps(capabilities.data);
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }, []);

  useEffect(() => {
    let active = true;
    void probeBrowserSupport().then((support) => {
      if (active) setBrowser(support);
    });
    void refresh();
    const stop = pollMs > 0 ? pollWhileVisible(() => void refresh(), pollMs) : () => {};
    return () => {
      active = false;
      stop();
    };
  }, [refresh, pollMs]);

  const saved = useMemo(() => (status ? savedDraft(status, mode) : undefined), [status, mode]);
  return { admin: account.role === 'admin', status, caps, browser, saved, failed, refresh };
}

// Publish applied shared settings to the page: mouse mapping follows the
// stream resolution, and the recorder follows the frame rate.
export function useSyncStreamAtoms() {
  const setResolution = useSetAtom(resolutionAtom);
  const setFps = useSetAtom(streamFpsAtom);
  const setGop = useSetAtom(streamGopAtom);
  const setQuality = useSetAtom(streamQualityAtom);
  return useCallback(
    (draft: VideoDraft, caps: VideoCapabilities) => {
      const width = caps.stream.limits.find((l) => l.height === draft.height)?.width ?? 0;
      setResolution({ width, height: draft.height });
      setFps(draft.fps);
      setGop(draft.gop);
      const value = draft.transport === 'mjpeg' ? draft.quality : draft.bitRate;
      const index = [...(getQualityMap(storedMode(draft.transport)) ?? [])].find(
        ([, v]) => v === value
      )?.[0];
      if (index !== undefined) setQuality(index);
    },
    [setResolution, setFps, setGop, setQuality]
  );
}
