import { useCallback, useEffect, useMemo, useState } from 'react';
import { useAuth } from '@/contexts/auth';
import type { TFunction } from 'i18next';
import { useAtomValue, useSetAtom } from 'jotai';

import type { ScreenStatus } from '@/api/vm';
import { getScreen, getVideoCapabilities } from '@/api/vm';
import { getEncoderCodec, isEncoderCodecSupported } from '@/lib/encoder';
import * as storage from '@/lib/localstorage';
import {
  videoPath,
  type BrowserSupport,
  type DirectCapture,
  type Transport,
  type VideoCapabilities,
  type VideoDraft
} from '@/lib/video-model';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { resolutionAtom, streamFpsAtom, videoModeAtom } from '@/jotai/screen';

// "Straight from capture", or "Through the video processor (VPSS): <why>".
export function videoPathText(t: TFunction, path: number | undefined) {
  const where = videoPath(path);
  if (!where) return '';
  return where === 'direct'
    ? t('videoSettings.pathDirect')
    : t('videoSettings.pathVpss', { reason: t(`videoSettings.pathReason.${where}`) });
}

// Shared settings and status returned by GET /api/vm/screen.

const transportOf = (mode: string): Transport =>
  mode === 'h264' ? 'webrtc' : mode === 'mjpeg' ? 'mjpeg' : 'direct';

// The codecs that "straight from capture" applies to on this device.
function directCaptureOf(status: ScreenStatus): DirectCapture {
  return { h264: status.viDirectH264Supported === true, h265: status.hevcDirectSupported === true };
}

function savedDraft(status: ScreenStatus, mode: string): VideoDraft {
  return {
    monitor: status.monitor,
    portrait: status.portrait ? status.portraitResolution : 0,
    height: status.height,
    fps: status.fps,
    transport: transportOf(mode),
    codec: getEncoderCodec(),
    bitRate: status.bitRate,
    quality: status.quality,
    mjpegChroma: status.mjpegChroma === 422 ? 422 : 420,
    hevcDirect:
      status.hevcDirectSupported || status.viDirectH264Supported === true
        ? status.hevcDirect !== 0
        : null,
    directPlayback: storage.getDirectPlayback(),
    frameDetect: status.frameDetect === true
  };
}

let browserSupport: Promise<BrowserSupport> | undefined;

// What this browser plays; probed once per page.
function probeBrowserSupport() {
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
  const directCapture = useMemo(() => (status ? directCaptureOf(status) : undefined), [status]);
  return {
    admin: account.role === 'admin',
    status,
    caps,
    browser,
    saved,
    directCapture,
    failed,
    refresh
  };
}

// Publish applied shared settings to the page: mouse mapping follows the
// stream resolution, and the recorder follows the frame rate.
export function useSyncStreamAtoms() {
  const setResolution = useSetAtom(resolutionAtom);
  const setFps = useSetAtom(streamFpsAtom);
  return useCallback(
    (draft: VideoDraft, caps: VideoCapabilities) => {
      const width = caps.stream.limits.find((l) => l.height === draft.height)?.width ?? 0;
      setResolution({ width, height: draft.height });
      setFps(draft.fps);
    },
    [setResolution, setFps]
  );
}
