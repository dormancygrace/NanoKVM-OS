import { useEffect, useRef, useState } from 'react';
import { Alert } from 'antd';
import clsx from 'clsx';
import { useAtomValue, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { encoderCodecQuery, getEncoderCodec, isEncoderCodecSupported } from '@/lib/encoder.ts';
import { getDirectPlayback } from '@/lib/localstorage';
import { getBaseUrl } from '@/lib/service.ts';
import { mouseStyleAtom } from '@/jotai/mouse';
import { screenshotSourceAtom } from '@/jotai/screen.ts';

import DirectWorker from './direct.worker.ts?worker';
import { ScreenViewport } from './viewport.tsx';

export const H264Direct = ({ onEncoderConflict }: { onEncoderConflict: () => boolean }) => {
  const { t } = useTranslation();
  const mouseStyle = useAtomValue(mouseStyleAtom);
  const setScreenshotSource = useSetAtom(screenshotSourceAtom);
  const [fatalError, setFatalError] = useState<string | null>(null);

  const canvasRef = useRef<HTMLCanvasElement>(null);
  const workerRef = useRef<Worker | null>(null);
  const screenshotRequests = useRef(
    new Map<
      number,
      {
        resolve: (blob: Blob) => void;
        reject: (error: Error) => void;
        timer: ReturnType<typeof setTimeout>;
      }
    >()
  );
  const nextScreenshotRequest = useRef(1);
  const translationRef = useRef(t);

  useEffect(() => {
    translationRef.current = t;
  }, [t]);

  useEffect(() => {
    let disposed = false;
    const pendingScreenshots = screenshotRequests.current;
    const requestedCodec = getEncoderCodec();
    setFatalError(null);

    void isEncoderCodecSupported('direct', requestedCodec).then((supported) => {
      if (disposed) return;
      if (!window.VideoDecoder) {
        setFatalError(translationRef.current('screen.encoderUnsupported'));
        return;
      }
      if (!canvasRef.current) return;

      const codec = supported ? requestedCodec : 'h264';

      const worker = new DirectWorker();
      workerRef.current = worker;
      const offscreen = canvasRef.current.transferControlToOffscreen();
      const query = encoderCodecQuery(codec);
      const url = `${getBaseUrl('ws')}/api/stream/video/direct?${query}`;
      const diagnosticParams = new URLSearchParams(window.location.search);
      const diagnostics = diagnosticParams.get('directStats') === '1';
      const requestedDecoder = diagnosticParams.get('directDecoder');
      const decoderPreference =
        diagnostics && requestedDecoder === 'software'
          ? 'prefer-software'
          : diagnostics && requestedDecoder === 'default'
            ? 'no-preference'
            : diagnostics && requestedDecoder === 'hardware'
              ? 'prefer-hardware'
              : undefined;
      const requestedRender = diagnosticParams.get('directRender');
      const renderMode =
        requestedRender === 'immediate' || requestedRender === 'vsync'
          ? requestedRender
          : getDirectPlayback();
      const playoutDelayMs = diagnosticParams.has('directBufferMs')
        ? Number(diagnosticParams.get('directBufferMs'))
        : undefined;
      const flowControl = !diagnostics || diagnosticParams.get('directFlow') !== 'off';
      worker.onmessage = (
        event: MessageEvent<{
          type?: string;
          width?: number;
          height?: number;
          code?: string;
          detail?: string;
          requestId?: number;
          blob?: Blob;
          stats?: {
            seconds: number;
            counts: Record<string, number>;
            renderMode: string;
            decoderPreference?: string;
          };
        }>
      ) => {
        const { type, width, height, code, detail } = event.data;
        if (type === 'screenshot-result' && event.data.requestId) {
          const pending = pendingScreenshots.get(event.data.requestId);
          if (!pending) return;
          pendingScreenshots.delete(event.data.requestId);
          clearTimeout(pending.timer);
          if (event.data.blob) pending.resolve(event.data.blob);
          else
            pending.reject(
              new Error(code === 'no-frame' ? 'screenshot-no-video' : 'screenshot-encode-failed')
            );
          return;
        }
        if (type === 'direct-stats' && diagnostics && event.data.stats && canvasRef.current) {
          canvasRef.current.dataset.directStats = JSON.stringify(event.data.stats);
          const output = document.getElementById('direct-diagnostics');
          if (output)
            output.textContent = `Direct ${event.data.stats.renderMode} (${event.data.stats.decoderPreference ?? 'prefer-hardware'}): received ${event.data.stats.counts.received ?? 0}, decoded ${event.data.stats.counts.decoded ?? 0}, painted ${event.data.stats.counts.painted ?? 0} / ${event.data.stats.seconds.toFixed(2)}s`;
          return;
        }
        if (type === 'stream-error') {
          if (code === 'encoder-conflict' && onEncoderConflict()) return;
          if (detail) console.error('Direct video stream rejected:', detail);
          setFatalError(
            translationRef.current(
              code === 'unsupported-codec' ? 'screen.encoderUnsupported' : 'screen.encoderConflict'
            )
          );
          return;
        }
        if (type !== 'frame-size' || !width || !height || !canvasRef.current) return;

        canvasRef.current.dataset.mediaWidth = String(width);
        canvasRef.current.dataset.mediaHeight = String(height);
        setScreenshotSource({
          width,
          height,
          capture: () =>
            new Promise<Blob>((resolve, reject) => {
              const activeWorker = workerRef.current;
              if (!activeWorker) {
                reject(new Error('screenshot-no-video'));
                return;
              }
              const requestId = nextScreenshotRequest.current++;
              const timer = setTimeout(() => {
                pendingScreenshots.delete(requestId);
                reject(new Error('screenshot-encode-failed'));
              }, 5000);
              pendingScreenshots.set(requestId, { resolve, reject, timer });
              activeWorker.postMessage({ type: 'screenshot', requestId });
            })
        });
      };
      worker.postMessage(
        {
          type: 'video',
          codec,
          canvas: offscreen,
          url,
          diagnostics,
          decoderPreference,
          renderMode,
          playoutDelayMs,
          flowControl
        },
        [offscreen]
      );
    });

    return () => {
      disposed = true;
      const worker = workerRef.current;
      workerRef.current = null;
      if (worker) {
        worker.postMessage({ type: 'stop' });
        worker.terminate();
      }
      setScreenshotSource(null);
      pendingScreenshots.forEach(({ reject, timer }) => {
        clearTimeout(timer);
        reject(new Error('screenshot-no-video'));
      });
      pendingScreenshots.clear();
    };
  }, [onEncoderConflict, setScreenshotSource]);

  return (
    <div className="relative h-full min-h-0 w-full min-w-0 overflow-hidden">
      <ScreenViewport>
        <canvas
          id="screen"
          ref={canvasRef}
          className={clsx('block touch-none select-none', mouseStyle)}
        ></canvas>
      </ScreenViewport>
      {new URLSearchParams(window.location.search).get('directStats') === '1' && (
        <output
          id="direct-diagnostics"
          className="pointer-events-none absolute bottom-1 left-1 bg-black/70 px-2 text-xs text-white"
        />
      )}
      {fatalError && (
        <Alert
          className="absolute top-6 left-1/2 z-50 max-w-[min(90%,560px)] -translate-x-1/2"
          type="error"
          showIcon
          message={t('screen.encoderError')}
          description={fatalError}
        />
      )}
    </div>
  );
};
