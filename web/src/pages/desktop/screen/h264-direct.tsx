import { useEffect, useRef, useState } from 'react';
import { Alert } from 'antd';
import clsx from 'clsx';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { encoderCodecQuery, getEncoderCodec, isEncoderCodecSupported } from '@/lib/encoder.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { mouseStyleAtom } from '@/jotai/mouse';

import DirectWorker from './direct.worker.ts?worker';
import { ScreenViewport } from './viewport.tsx';

export const H264Direct = () => {
  const { t } = useTranslation();
  const mouseStyle = useAtomValue(mouseStyleAtom);
  const [fatalError, setFatalError] = useState<string | null>(null);

  const canvasRef = useRef<HTMLCanvasElement>(null);
  const workerRef = useRef<Worker | null>(null);
  const translationRef = useRef(t);

  useEffect(() => {
    translationRef.current = t;
  }, [t]);

  useEffect(() => {
    let disposed = false;
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
        requestedRender === 'immediate' || requestedRender === 'vsync' ? requestedRender : 'paced';
      const playoutDelayMs = Number(diagnosticParams.get('directBufferMs') || 35);
      const flowControl = !diagnostics || diagnosticParams.get('directFlow') !== 'off';
      worker.onmessage = (
        event: MessageEvent<{
          type?: string;
          width?: number;
          height?: number;
          code?: string;
          detail?: string;
          stats?: {
            seconds: number;
            counts: Record<string, number>;
            renderMode: string;
            decoderPreference?: string;
          };
        }>
      ) => {
        const { type, width, height, code, detail } = event.data;
        if (type === 'direct-stats' && diagnostics && event.data.stats && canvasRef.current) {
          canvasRef.current.dataset.directStats = JSON.stringify(event.data.stats);
          const output = document.getElementById('direct-diagnostics');
          if (output)
            output.textContent = `Direct ${event.data.stats.renderMode} (${event.data.stats.decoderPreference ?? 'prefer-hardware'}): received ${event.data.stats.counts.received ?? 0}, decoded ${event.data.stats.counts.decoded ?? 0}, painted ${event.data.stats.counts.painted ?? 0} / ${event.data.stats.seconds.toFixed(2)}s`;
          return;
        }
        if (type === 'stream-error') {
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
    };
  }, []);

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
          className="absolute left-1/2 top-6 z-50 max-w-[min(90%,560px)] -translate-x-1/2"
          type="error"
          showIcon
          message={t('screen.encoderError')}
          description={fatalError}
        />
      )}
    </div>
  );
};
