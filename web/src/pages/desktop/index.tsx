import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Splitter } from 'antd';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';
import { useMediaQuery } from 'react-responsive';

import { getEncoderState } from '@/api/stream.ts';
import { getInputRegion, getScreen, setControlRegionMode } from '@/api/vm.ts';
import { ControlRegionConfig, InputRegion } from '@/types';
import { refreshCapture } from '@/lib/capture-control.ts';
import { getEncoderCodec, initializeEncoderCodec } from '@/lib/encoder.ts';
import * as storage from '@/lib/localstorage.ts';
import { client } from '@/lib/websocket.ts';
import { picoclawChatOpenAtom } from '@/jotai/picoclaw.ts';
import {
  controlRegionModeAtom,
  inputRegionAtom,
  isHdmiEnabledAtom,
  manualInputRegionAtom,
  manualRegionsAtom,
  resolutionAtom,
  selectedManualRegionAtom,
  selectedOriginalResolutionAtom,
  videoModeAtom
} from '@/jotai/screen.ts';
import { Head } from '@/components/head.tsx';

import { CaptureStatusOverlay, useCaptureStatus } from './capture-status';
import { CapturePaused } from './capture-status/paused';
import { Keyboard } from './keyboard';
import { Menu } from './menu';
import { Mouse } from './mouse';
import { H264ModeNotification, Notification } from './notification.tsx';
import { Sidebar as PicoclawSidebar } from './picoclaw';
import { ActionOverlay } from './picoclaw/action-overlay.tsx';
import { Screen } from './screen';
import { AutoRegion } from './screen/auto-region.tsx';
import {
  getMediaSize,
  isInputRegionCompatible,
  isMediaReady,
  isValidInputRegion
} from './screen/geometry.ts';
import { InputRegionOverlay } from './screen/input-region-overlay.tsx';
import { ManualRegion } from './screen/manual-region.tsx';
import { VirtualKeyboard } from './virtual-keyboard';

function getVideoMode() {
  const directSupported = window.isSecureContext && !!window.VideoDecoder;
  const defaultVideoMode = directSupported ? 'direct' : window.RTCPeerConnection ? 'h264' : 'mjpeg';

  const cookieVideoMode = storage.getVideoMode();
  if (!cookieVideoMode || (cookieVideoMode === 'direct' && !directSupported)) {
    return defaultVideoMode;
  }

  return ['direct', 'h264', 'mjpeg'].includes(cookieVideoMode) ? cookieVideoMode : defaultVideoMode;
}

export const Desktop = () => {
  const { t } = useTranslation();
  const isBigScreen = useMediaQuery({ minWidth: 850 });
  const [activeVideoMode] = useState(getVideoMode);
  const [encoderReady, setEncoderReady] = useState(false);
  const [encoderError, setEncoderError] = useState<string | null>(null);
  const [joinAttempt, setJoinAttempt] = useState(0);
  const joinRetries = useRef(0);
  const onEncoderConflict = useCallback(() => {
    // Handle a simultaneous join between the state read and subscription,
    // without page reloads or an unbounded reconnect loop.
    if (joinRetries.current >= 2) return false;
    joinRetries.current++;
    setEncoderReady(false);
    setJoinAttempt((attempt) => attempt + 1);
    return true;
  }, []);
  const [picoclawSidebarWidth, setPicoclawSidebarWidth] = useState(420);
  const captureStatus = useCaptureStatus(activeVideoMode);
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  useEffect(() => {
    const refresh = () => {
      void refreshCapture().catch(() => undefined);
    };
    refresh();
    const timer = setInterval(refresh, 3000);
    window.addEventListener('focus', refresh);
    return () => {
      clearInterval(timer);
      window.removeEventListener('focus', refresh);
    };
  }, []);

  const [videoMode, setVideoMode] = useAtom(videoModeAtom);
  const [resolution, setResolution] = useAtom(resolutionAtom);
  const [inputRegion, setInputRegion] = useAtom(inputRegionAtom);
  const [controlRegionMode, setControlRegionModeState] = useAtom(controlRegionModeAtom);
  const setManualInputRegion = useSetAtom(manualInputRegionAtom);
  const setManualRegions = useSetAtom(manualRegionsAtom);
  const setSelectedManualRegion = useSetAtom(selectedManualRegionAtom);
  const setSelectedOriginalResolution = useSetAtom(selectedOriginalResolutionAtom);
  const isPicoclawChatOpen = useAtomValue(picoclawChatOpenAtom);

  useEffect(() => {
    let active = true;
    setEncoderReady(false);
    setEncoderError(null);
    const join = async () => {
      if (activeVideoMode === 'mjpeg') return;
      const rsp = await getEncoderState();
      if (!active) return;
      if (rsp.code !== 0) throw new Error('encoder-state-failed');
      const codec = rsp.data?.active ? rsp.data.codec : undefined;
      if (codec !== undefined && codec !== 'h264' && codec !== 'h265') {
        throw new Error('encoder-state-failed');
      }
      await initializeEncoderCodec(activeVideoMode === 'h264' ? 'webrtc' : 'direct', codec);
    };
    void join()
      .catch((error: unknown) => {
        if (!active) return;
        setEncoderError(
          error instanceof Error && error.message === 'active-codec-unsupported'
            ? 'screen.activeEncoderUnsupported'
            : 'screen.encoderStateFailed'
        );
      })
      .finally(() => {
        if (!active) return;
        setVideoMode(activeVideoMode);
        setEncoderReady(true);
      });
    return () => {
      active = false;
    };
  }, [activeVideoMode, joinAttempt, setVideoMode]);

  useEffect(() => {
    client.connect();

    // Monitor timing is device-wide. Opening a tab must never apply its cached
    // resolution to the HDMI source or interrupt another viewer.
    let active = true;

    setResolution(null);
    getScreen()
      .then((rsp) => {
        if (!active) return;
        const res =
          rsp.code === 0
            ? { width: rsp.data.width, height: rsp.data.height }
            : { width: 0, height: 0 };
        storage.setResolution(res);
        setResolution(res);
      })
      .catch(() => {
        if (active) setResolution({ width: 0, height: 0 });
      });
    setInputRegion(null);
    setManualInputRegion(null);
    setManualRegions([]);
    setSelectedManualRegion('');
    setSelectedOriginalResolution('');
    setControlRegionModeState('off');

    getInputRegion()
      .then((rsp) => {
        const config = rsp.data as ControlRegionConfig | null;
        const mode = config?.mode || 'off';
        const manualRegion = isValidInputRegion(config as InputRegion)
          ? (config as InputRegion)
          : null;
        const selectedResolution = config?.selectedResolution || '';
        const regions = config?.regions || [];
        const selectedRegion = config?.selectedRegion || '';
        const selectedManualRegion = regions.find(
          (region) => `${region.width}x${region.height}` === selectedRegion
        );
        setManualInputRegion(manualRegion);
        setManualRegions(regions);
        setSelectedManualRegion(selectedRegion);
        setSelectedOriginalResolution(selectedResolution);
        setInputRegion(mode === 'manual' && selectedRegion ? selectedManualRegion || null : null);
        setControlRegionModeState(mode);
      })
      .catch(() => {
        setControlRegionModeState('off');
        setInputRegion(null);
        setManualInputRegion(null);
        setManualRegions([]);
        setSelectedManualRegion('');
        setSelectedOriginalResolution('');
      });

    return () => {
      active = false;
      client.close();
    };
  }, [
    activeVideoMode,
    setControlRegionModeState,
    setInputRegion,
    setManualInputRegion,
    setManualRegions,
    setResolution,
    setSelectedManualRegion,
    setSelectedOriginalResolution,
    setVideoMode
  ]);

  useEffect(() => {
    if (controlRegionMode !== 'manual' || !inputRegion) {
      return;
    }

    const screen = document.getElementById('screen');
    if (!screen) {
      return;
    }
    const target = screen;
    const region = inputRegion;

    let cleared = false;
    let validationTimer: ReturnType<typeof setTimeout> | null = null;
    function validateMediaSize() {
      if (cleared) {
        return;
      }

      if (validationTimer !== null) {
        clearTimeout(validationTimer);
      }
      validationTimer = setTimeout(() => {
        const mediaSize = getMediaSize(target, resolution);
        if (!mediaSize || !isMediaReady(target) || isInputRegionCompatible(region, mediaSize)) {
          return;
        }

        cleared = true;
        setControlRegionMode('off')
          .then((rsp) => {
            if (rsp.code === 0) {
              setControlRegionModeState('off');
              setInputRegion(null);
            }
          })
          .catch(() => undefined);
      }, 300);
    }

    validateMediaSize();
    const observer = new MutationObserver(validateMediaSize);
    observer.observe(target, {
      attributes: true,
      attributeFilter: ['data-media-width', 'data-media-height']
    });
    target.addEventListener('load', validateMediaSize);
    target.addEventListener('loadedmetadata', validateMediaSize);
    target.addEventListener('canplay', validateMediaSize);
    target.addEventListener('resize', validateMediaSize);

    return () => {
      observer.disconnect();
      if (validationTimer !== null) {
        clearTimeout(validationTimer);
      }
      target.removeEventListener('load', validateMediaSize);
      target.removeEventListener('loadedmetadata', validateMediaSize);
      target.removeEventListener('canplay', validateMediaSize);
      target.removeEventListener('resize', validateMediaSize);
    };
  }, [
    controlRegionMode,
    inputRegion,
    resolution,
    setControlRegionModeState,
    setInputRegion,
    videoMode
  ]);

  function handleSplitterResize(sizes: number[]) {
    const nextSidebarWidth = sizes[1];
    if (typeof nextSidebarWidth === 'number' && nextSidebarWidth > 0) {
      setPicoclawSidebarWidth(nextSidebarWidth);
    }
  }

  return (
    <div className="h-screen w-screen overflow-hidden bg-neutral-950">
      <Head title={t('head.desktop')} />

      {isBigScreen && <Notification />}
      <H264ModeNotification />

      {encoderReady && videoMode && resolution && (
        <div className="relative flex h-full min-h-0 w-full min-w-0">
          <Menu />
          <div className="h-full min-h-0 w-full min-w-0">
            <Splitter
              className="h-full w-full"
              style={{ height: '100%', width: '100%' }}
              onResize={handleSplitterResize}
            >
              <Splitter.Panel min="45%">
                <div className="relative h-full min-h-0 w-full min-w-0 overflow-hidden bg-black">
                  {captureEnabled && !encoderError && (
                    <Screen onEncoderConflict={onEncoderConflict} />
                  )}
                  {captureEnabled && encoderError && (
                    <Alert
                      className="absolute left-1/2 top-6 z-50 w-[min(90%,560px)] -translate-x-1/2"
                      type="warning"
                      showIcon
                      message={t('screen.encoderError')}
                      description={t(encoderError, {
                        codec: getEncoderCodec() === 'h265' ? 'H.265' : 'H.264'
                      })}
                      action={
                        <Button
                          onClick={() => {
                            joinRetries.current = 0;
                            setJoinAttempt((attempt) => attempt + 1);
                          }}
                        >
                          {t('screen.retryJoin')}
                        </Button>
                      }
                    />
                  )}
                  {captureEnabled ? (
                    <CaptureStatusOverlay status={captureStatus} />
                  ) : (
                    <CapturePaused />
                  )}
                </div>
              </Splitter.Panel>
              <Splitter.Panel
                size={isBigScreen && isPicoclawChatOpen ? picoclawSidebarWidth : 0}
                min={isBigScreen && isPicoclawChatOpen ? 340 : 0}
                max="45%"
                resizable={isBigScreen && isPicoclawChatOpen}
              >
                {isBigScreen && isPicoclawChatOpen ? <PicoclawSidebar /> : null}
              </Splitter.Panel>
            </Splitter>
          </div>
          {captureEnabled && (
            <>
              <ActionOverlay />
              <AutoRegion />
              <ManualRegion />
              <InputRegionOverlay />
              <Mouse />
              <Keyboard />
            </>
          )}
        </div>
      )}

      {!isBigScreen && isPicoclawChatOpen ? (
        <div className="fixed inset-x-0 bottom-0 top-14 z-[980] overflow-hidden bg-[#0d0d0f] shadow-2xl">
          <PicoclawSidebar />
        </div>
      ) : null}

      {captureEnabled && <VirtualKeyboard />}
    </div>
  );
};
