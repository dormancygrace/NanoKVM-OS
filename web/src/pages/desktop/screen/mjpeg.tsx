import { useCallback, useEffect, useRef, useState } from 'react';
import clsx from 'clsx';
import { useAtomValue, useSetAtom } from 'jotai';

import { stopFrameDetect } from '@/api/stream.ts';
import { getVideoCapabilities } from '@/api/vm.ts';
import { captureMediaElement } from '@/lib/screenshot.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { mouseStyleAtom } from '@/jotai/mouse.ts';
import { resolutionAtom, screenshotSourceAtom } from '@/jotai/screen.ts';

import { parseMjpegTiles } from './mjpeg-tiles.ts';
import { ScreenViewport } from './viewport.tsx';

const INITIAL_RETRY_DELAY = 1000;
const MAX_RETRY_DELAY = 10000;
const STREAM_HEALTH_INTERVAL = 1000;
// Messages waiting to be decoded before the viewer starts again from a whole
// picture (the server sends one to every new connection).
const MAX_PENDING_MESSAGES = 30;

// Tiles over a WebSocket: only the parts of the picture that changed, where
// the server says it sends them; otherwise the classic stream of whole JPEGs.
export const Mjpeg = () => {
  const [tiles, setTiles] = useState<boolean | null>(null);
  useEffect(() => {
    let active = true;
    getVideoCapabilities()
      .then((rsp) => active && setTiles(rsp.code === 0 && rsp.data?.stream?.mjpeg?.tiles === true))
      .catch(() => active && setTiles(false));
    return () => {
      active = false;
    };
  }, []);
  if (tiles === null) return null;
  return tiles ? <MjpegTiles /> : <MjpegImage />;
};

const MjpegTiles = () => {
  const resolution = useAtomValue(resolutionAtom);
  const mouseStyle = useAtomValue(mouseStyleAtom);
  const setScreenshotSource = useSetAtom(screenshotSourceAtom);
  const canvasRef = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const context = canvas?.getContext('2d', { alpha: false });
    if (!canvas || !context) return;
    void stopFrameDetect(10).catch(() => {});
    setScreenshotSource(null);

    let socket: WebSocket | null = null;
    let disposed = false;
    let retryDelay = INITIAL_RETRY_DELAY;
    let retryTimer: number | undefined;
    // Messages build on each other: decode in parallel, draw in order.
    let drawn = Promise.resolve();
    let pending = 0;

    const publishSize = (width: number, height: number) => {
      canvas.width = width;
      canvas.height = height;
      canvas.dataset.mediaWidth = String(width);
      canvas.dataset.mediaHeight = String(height);
      setScreenshotSource({
        width,
        height,
        capture: () =>
          new Promise<Blob>((resolve, reject) =>
            canvas.toBlob(
              (blob) => (blob ? resolve(blob) : reject(new Error('screenshot-encode-failed'))),
              'image/png'
            )
          )
      });
    };

    const connect = () => {
      const generation = (socket = new WebSocket(`${getBaseUrl('ws')}/api/stream/mjpeg/tiles`));
      let waitWhole = true;
      generation.binaryType = 'arraybuffer';

      generation.onmessage = (event: MessageEvent<ArrayBuffer>) => {
        if (disposed || socket !== generation) return;
        const message = parseMjpegTiles(event.data);
        if (!message || (waitWhole && !message.whole)) return;
        waitWhole = false;
        retryDelay = INITIAL_RETRY_DELAY;
        if (++pending > MAX_PENDING_MESSAGES) {
          // Decoding cannot keep up: start again from a whole picture.
          generation.close();
          return;
        }
        const bitmaps = Promise.all(
          message.tiles.map((tile) =>
            createImageBitmap(new Blob([tile.jpeg], { type: 'image/jpeg' })).then((bitmap) => ({
              tile,
              bitmap
            }))
          )
        );
        drawn = drawn
          .then(() => bitmaps)
          .then((decoded) => {
            pending--;
            if (disposed || socket !== generation) {
              decoded.forEach(({ bitmap }) => bitmap.close());
              return;
            }
            if (canvas.width !== message.width || canvas.height !== message.height) {
              publishSize(message.width, message.height);
            }
            for (const { tile, bitmap } of decoded) {
              context.drawImage(bitmap, tile.x, tile.y, tile.width, tile.height);
              bitmap.close();
            }
          })
          .catch(() => {
            pending--;
            // A tile that did not decode leaves the picture behind.
            if (socket === generation) generation.close();
          });
      };

      generation.onclose = () => {
        if (disposed || socket !== generation) return;
        socket = null;
        retryTimer = window.setTimeout(connect, retryDelay);
        retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY);
      };
    };
    connect();

    return () => {
      disposed = true;
      window.clearTimeout(retryTimer);
      socket?.close();
      socket = null;
      setScreenshotSource(null);
    };
  }, [resolution, setScreenshotSource]);

  return (
    <ScreenViewport>
      <canvas
        ref={canvasRef}
        id="screen"
        className={clsx('block touch-none select-none', mouseStyle)}
      ></canvas>
    </ScreenViewport>
  );
};

const MjpegImage = () => {
  const resolution = useAtomValue(resolutionAtom);
  const mouseStyle = useAtomValue(mouseStyleAtom);
  const setScreenshotSource = useSetAtom(screenshotSourceAtom);
  const [hasError, setHasError] = useState(false);
  const [streamNonce, setStreamNonce] = useState(0);
  const image = useRef<HTMLImageElement>(null);
  const retryDelay = useRef(INITIAL_RETRY_DELAY);
  const retryTimer = useRef<number | undefined>(undefined);
  const streamURL = `${getBaseUrl('http')}/api/stream/mjpeg`;
  const streamSrc = hasError ? undefined : `${streamURL}?v=${streamNonce}`;

  const reconnect = useCallback(() => {
    if (retryTimer.current !== undefined) {
      return;
    }

    setHasError(true);

    retryTimer.current = window.setTimeout(() => {
      retryTimer.current = undefined;
      setStreamNonce((current) => current + 1);
      setHasError(false);
    }, retryDelay.current);
    retryDelay.current = Math.min(retryDelay.current * 2, MAX_RETRY_DELAY);
  }, []);

  const resetRetryDelay = useCallback(() => {
    retryDelay.current = INITIAL_RETRY_DELAY;
  }, []);

  const markFrameReady = useCallback(() => {
    resetRetryDelay();
    const element = image.current;
    if (!element?.naturalWidth || !element.naturalHeight) return;
    setScreenshotSource({
      width: element.naturalWidth,
      height: element.naturalHeight,
      capture: () => captureMediaElement(element)
    });
  }, [resetRetryDelay, setScreenshotSource]);

  useEffect(() => () => setScreenshotSource(null), [setScreenshotSource]);

  useEffect(() => {
    // Pause frame detect for a while so the first frames arrive. The server
    // ignores this when frame detect is off, so it needs no local state.
    void stopFrameDetect(10).catch(() => {});
    window.clearTimeout(retryTimer.current);
    retryTimer.current = undefined;
    retryDelay.current = INITIAL_RETRY_DELAY;
    setScreenshotSource(null);
    setHasError(false);
    setStreamNonce((current) => current + 1);

    return () => window.clearTimeout(retryTimer.current);
  }, [resolution, setScreenshotSource]);

  useEffect(() => {
    const healthTimer = window.setInterval(() => {
      if (image.current?.complete && image.current.naturalWidth === 0 && !hasError) {
        reconnect();
      }
    }, STREAM_HEALTH_INTERVAL);

    return () => window.clearInterval(healthTimer);
  }, [hasError, reconnect]);

  return (
    <ScreenViewport>
      <img
        ref={image}
        id="screen"
        className={clsx('block touch-none select-none', mouseStyle)}
        style={{
          visibility: hasError ? 'hidden' : 'visible'
        }}
        src={streamSrc}
        onLoad={markFrameReady}
        onError={() => {
          setScreenshotSource(null);
          reconnect();
        }}
        alt="screen"
      />
    </ScreenViewport>
  );
};
