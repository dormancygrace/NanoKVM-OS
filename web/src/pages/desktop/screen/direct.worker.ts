import Queue from 'yocto-queue';

import { DIRECT_H264_CODEC, DIRECT_H265_CODEC } from '@/lib/encoder.ts';

import { DirectMetrics } from './direct-metrics.ts';
import { DirectPlayout } from './direct-playout.ts';

let canvas: OffscreenCanvas | null = null;
let ctx: OffscreenCanvasRenderingContext2D | null = null;
let rendering: boolean = false;
let flushScheduled: boolean = false;
let decoder: VideoDecoder | null = null;
let streamUrl: string | null = null;
let socket: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectDelayMs = 250;
let stopped = false;
let resyncRequested = false;
let pendingAckTimestamp: number | null = null;
let decodeBackpressured = false;
let reportedFrameWidth = 0;
let reportedFrameHeight = 0;
let renderMode: 'immediate' | 'vsync' | 'paced' = 'paced';
let playout = new DirectPlayout<VideoFrame>();
let metrics: DirectMetrics | null = null;
let animationRequest: number | null = null;
let reportTimer: ReturnType<typeof setInterval> | null = null;
let paintSerial = 0;
let observedPaintSerial = 0;
let lastDisplayedTimestamp: number | null = null;
let paintedTimestamp: number | null = null;
let playoutDelayMs = 35;
let flowControl = true;
let decoderPreference: 'prefer-hardware' | 'prefer-software' | 'no-preference' = 'prefer-hardware';

let allowHardwareFallback = false;

const maxQueuedFrames = 1;
const maxReconnectDelayMs = 5_000;
const frameAckMessage = 2;
const streamResyncMessage = 3;
const flowControlWindow = 8;
const decoderHighWatermark = 6;
const decoderLowWatermark = 3;
const maxPendingDecodes = 16;
const frameQueue = new Queue<VideoFrame>();
const frameChannel = new MessageChannel();

type WorkerMessage = {
  type: 'video' | 'stop';
  codec?: 'h264' | 'h265';
  canvas?: OffscreenCanvas;
  url?: string;
  renderMode?: 'immediate' | 'vsync' | 'paced';
  diagnostics?: boolean;
  decoderPreference?: 'prefer-hardware' | 'prefer-software' | 'no-preference';
  playoutDelayMs?: number;
  flowControl?: boolean;
};

let codec: 'h264' | 'h265' = 'h264';

frameChannel.port1.onmessage = () => {
  flushScheduled = false;
  processFrameQueue();
};

self.onmessage = (event: MessageEvent<WorkerMessage>) => {
  const { type, codec: requestedCodec, canvas: offscreenCanvas, url } = event.data;

  switch (type) {
    case 'video': {
      if (!offscreenCanvas || !url || !requestedCodec) {
        return;
      }

      codec = requestedCodec;
      canvas = offscreenCanvas;
      reportedFrameWidth = 0;
      reportedFrameHeight = 0;
      renderMode = event.data.renderMode ?? 'paced';
      playoutDelayMs = [20, 35, 50].includes(event.data.playoutDelayMs ?? 35)
        ? (event.data.playoutDelayMs ?? 35)
        : 35;
      playout = new DirectPlayout<VideoFrame>(renderMode === 'paced' ? playoutDelayMs : 0);
      metrics = event.data.diagnostics ? new DirectMetrics() : null;
      const override = event.data.diagnostics ? event.data.decoderPreference : undefined;
      const explicitPreference =
        override === 'prefer-software' ||
        override === 'prefer-hardware' ||
        override === 'no-preference';
      // H.264 software preference avoids substantial decoder output buffering
      // observed in Chrome on Windows. Preferences remain browser hints.
      decoderPreference = explicitPreference
        ? override
        : codec === 'h264'
          ? 'prefer-software'
          : 'prefer-hardware';
      allowHardwareFallback = !explicitPreference && codec === 'h264';
      flowControl = event.data.flowControl !== false;
      ctx = canvas!.getContext('2d', {
        alpha: false,
        desynchronized: true
      }) as OffscreenCanvasRenderingContext2D;
      streamUrl = url;
      stopped = false;
      if (renderMode !== 'immediate' || metrics) startAnimation();
      if (metrics)
        reportTimer = setInterval(() => {
          self.postMessage({
            type: 'direct-stats',
            stats: metrics!.snapshot({
              renderMode,
              decoderPreference,
              playoutDelayMs,
              flowControl: Number(flowControl),
              decodeQueue: decoder?.decodeQueueSize ?? 0,
              renderQueue: playout.size,
              playoutDrops: playout.dropped
            })
          });
        }, 1000);
      connect();
      break;
    }
    case 'stop':
      stopped = true;
      clearReconnectTimer();
      disconnect();
      resetDecoder();
      if (animationRequest !== null) cancelAnimationFrame(animationRequest);
      animationRequest = null;
      if (reportTimer !== null) clearInterval(reportTimer);
      reportTimer = null;
      break;
  }
};

function connect() {
  if (stopped || !streamUrl || socket) {
    return;
  }

  try {
    const url = new URL(streamUrl);
    if (flowControl) url.searchParams.set('flow', String(flowControlWindow));
    const nextSocket = new WebSocket(url);
    nextSocket.binaryType = 'arraybuffer';
    socket = nextSocket;

    nextSocket.onopen = () => {
      if (socket !== nextSocket || stopped) {
        return;
      }

      reconnectDelayMs = 250;
      resyncRequested = false;
      pendingAckTimestamp = null;
      decodeBackpressured = false;
    };

    nextSocket.onmessage = (event) => {
      if (socket !== nextSocket || stopped || !(event.data instanceof ArrayBuffer)) {
        return;
      }

      handleWsMessage(event.data);
    };

    nextSocket.onerror = () => {
      if (socket === nextSocket) {
        nextSocket.close();
      }
    };

    nextSocket.onclose = (event) => {
      if (socket !== nextSocket) {
        return;
      }

      socket = null;
      resyncRequested = false;
      pendingAckTimestamp = null;
      decodeBackpressured = false;
      resetDecoder();

      if (event.code === 1008) {
        stopped = true;
        reportFatalError('encoder-conflict', event.reason);
        return;
      }
      scheduleReconnect();
    };
  } catch (error) {
    console.error(`Failed to create Direct ${codec.toUpperCase()} WebSocket:`, error);
    scheduleReconnect();
  }
}

function disconnect() {
  const currentSocket = socket;
  socket = null;

  if (currentSocket && currentSocket.readyState !== WebSocket.CLOSED) {
    currentSocket.close();
  }
}

function scheduleReconnect() {
  if (stopped || reconnectTimer !== null || !streamUrl) {
    return;
  }

  const delay = reconnectDelayMs;
  reconnectDelayMs = Math.min(reconnectDelayMs * 2, maxReconnectDelayMs);
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connect();
  }, delay);
}

function clearReconnectTimer() {
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
}

function handleWsMessage(message: ArrayBuffer) {
  try {
    if (message.byteLength < 9) {
      return;
    }

    const view = new DataView(message);
    const isKeyFrame = view.getUint8(0) === 1;
    const timestamp = Number(view.getBigUint64(1, true));
    metrics?.receive(timestamp);
    metrics?.event('source', timestamp / 1000);
    metrics?.count('receivedBytes', message.byteLength);
    if (isKeyFrame) metrics?.event('keyframes');
    const data = new Uint8Array(message, 9);

    // TCP already applies transport backpressure. Bound local decode work too:
    // a slow decoder must rejoin at an IDR, never accumulate a stale movie.
    if (decoder && decoder.decodeQueueSize >= maxPendingDecodes) {
      metrics?.count('decodeOverloads');
      requestStreamResync();
      resetDecoder();
      return;
    }

    if (!decoder) {
      if (!isKeyFrame) {
        requestStreamResync();
        return;
      }

      resyncRequested = false;
      const initial = createDecoder();
      if (!initial) {
        requestStreamResync();
        return;
      }

      decoder = initial;
      resyncRequested = false;
    }

    if (decoder?.state === 'configured') {
      decode(decoder, isKeyFrame, timestamp, data);
    }
  } catch (error) {
    console.error('Error processing WebSocket message in worker:', error);
  }
}

function createDecoder(): VideoDecoder | null {
  if (!self.VideoDecoder) {
    reportFatalError('unsupported-codec');
    stopped = true;
    disconnect();
    return null;
  }

  let instance: VideoDecoder | null = null;
  const init = {
    output: (frame: VideoFrame) => {
      handleDecodedFrame(instance, frame);
    },
    error: () => {
      if (decoder === instance) {
        fallbackToHardware();
        requestStreamResync();
        resetDecoder();
      }
    }
  };

  try {
    instance = new VideoDecoder(init);
    const configuredDecoder = instance;
    instance.ondequeue = () => {
      releaseDecodeBackpressure(configuredDecoder);
    };
    instance.configure({
      codec: codec === 'h265' ? DIRECT_H265_CODEC : DIRECT_H264_CODEC,
      hardwareAcceleration: decoderPreference,
      optimizeForLatency: true
    });
    return instance;
  } catch (err) {
    if (instance && instance.state !== 'closed') {
      instance.close();
    }
    if (fallbackToHardware()) return createDecoder();
    console.error(`Failed to configure the Direct ${codec.toUpperCase()} decoder:`, err);
    reportFatalError('unsupported-codec');
    stopped = true;
    disconnect();
    return null;
  }
}

// One-way for this stream, including reconnects: never oscillate after an error.
// Explicit diagnostic preferences must remain fixed for comparable measurements.
function fallbackToHardware(): boolean {
  if (!allowHardwareFallback || decoderPreference !== 'prefer-software') return false;
  allowHardwareFallback = false;
  decoderPreference = 'prefer-hardware';
  metrics?.count('decoderFallbacks');
  return true;
}

function reportFatalError(code: 'encoder-conflict' | 'unsupported-codec', detail?: string) {
  self.postMessage({ type: 'stream-error', code, detail });
}

function handleDecodedFrame(source: VideoDecoder | null, frame: VideoFrame) {
  if (!source) {
    frame.close();
    return;
  }

  if (source !== decoder) {
    frame.close();
    return;
  }

  metrics?.decode(frame.timestamp);
  if (renderMode !== 'immediate') {
    playout.push(frame, performance.now());
    return;
  }

  frameQueue.enqueue(frame);
  while (frameQueue.size > maxQueuedFrames) {
    const droppedFrame = frameQueue.dequeue();
    if (droppedFrame) {
      droppedFrame.close();
      metrics?.count('renderDropped');
    }
  }

  if (!rendering) {
    rendering = true;
    scheduleFrameQueue();
  }
}

function decode(target: VideoDecoder, isKeyFrame: boolean, timestamp: number, data: Uint8Array) {
  const chunk = new EncodedVideoChunk({
    type: isKeyFrame ? 'key' : 'delta',
    timestamp: timestamp,
    data: data
  });

  try {
    target.decode(chunk);
    pendingAckTimestamp = timestamp;
    if (target.decodeQueueSize >= decoderHighWatermark) {
      decodeBackpressured = true;
    }
    releaseDecodeBackpressure(target);
  } catch (err: any) {
    if (fallbackToHardware() || err.name === 'TypeError' || err.message?.includes('configured')) {
      requestStreamResync();
      resetDecoder();
    }
  }
}

function processFrameQueue() {
  const frame = frameQueue.dequeue();
  if (frame) {
    try {
      renderFrame(frame);
    } catch (error) {
      console.error(`Failed to render Direct ${codec.toUpperCase()} frame:`, error);
      requestStreamResync();
      resetDecoder();
      return;
    }
  }

  if (frameQueue.size > 0) {
    scheduleFrameQueue();
  } else {
    rendering = false;
  }
}

function scheduleFrameQueue() {
  if (flushScheduled) {
    return;
  }

  flushScheduled = true;
  frameChannel.port2.postMessage(null);
}

function startAnimation() {
  try {
    animationRequest = requestAnimationFrame(onAnimation);
  } catch {
    // Some worker owners cannot provide animation frames. Preserve playback.
    renderMode = 'immediate';
    playout.reset();
    metrics?.count('animationUnavailable');
  }
}

function onAnimation() {
  if (stopped) return;
  metrics?.event('refresh');
  if (paintSerial !== observedPaintSerial) {
    metrics?.event('displayUpdate');
    metrics?.count('overwrittenBeforeRefresh', Math.max(0, paintSerial - observedPaintSerial - 1));
    if (paintedTimestamp !== null && lastDisplayedTimestamp !== null) {
      metrics?.event('mediaAdvance', paintedTimestamp / 1000);
    }
    lastDisplayedTimestamp = paintedTimestamp;
    observedPaintSerial = paintSerial;
  }
  if (renderMode !== 'immediate') {
    const frame = playout.take(performance.now());
    if (frame) {
      try {
        renderFrame(frame);
      } catch {
        metrics?.count('renderErrors');
        requestStreamResync();
        resetDecoder();
      }
    }
  }
  startAnimation();
}

function renderFrame(frame: VideoFrame) {
  if (!canvas || !ctx) {
    frame.close();
    return;
  }

  try {
    if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
      canvas.width = frame.displayWidth;
      canvas.height = frame.displayHeight;
    }
    ctx.drawImage(frame, 0, 0, canvas.width, canvas.height);
    paintSerial++;
    paintedTimestamp = frame.timestamp;
    metrics?.paint(frame.timestamp);

    if (reportedFrameWidth !== frame.displayWidth || reportedFrameHeight !== frame.displayHeight) {
      reportedFrameWidth = frame.displayWidth;
      reportedFrameHeight = frame.displayHeight;
      self.postMessage({
        type: 'frame-size',
        width: reportedFrameWidth,
        height: reportedFrameHeight
      });
    }
  } finally {
    frame.close();
  }
}

function resetDecoder() {
  metrics?.count('decoderResets');
  playout.reset();
  if (decoder && decoder.state !== 'closed') {
    try {
      decoder.close();
    } catch (err) {
      console.log(err);
    }
  }

  decoder = null;
  pendingAckTimestamp = null;
  decodeBackpressured = false;
  rendering = false;
  flushScheduled = false;

  Array.from(frameQueue.drain()).forEach((frame) => frame.close());
}

function releaseDecodeBackpressure(source: VideoDecoder) {
  if (source !== decoder || pendingAckTimestamp === null) {
    return;
  }

  if (decodeBackpressured && source.decodeQueueSize > decoderLowWatermark) {
    return;
  }

  decodeBackpressured = false;
  acknowledgeFrame(pendingAckTimestamp);
  pendingAckTimestamp = null;
}

function acknowledgeFrame(timestamp: number) {
  if (!flowControl) return;
  const currentSocket = socket;
  if (!currentSocket || currentSocket.readyState !== WebSocket.OPEN) {
    return;
  }

  const message = new ArrayBuffer(9);
  const view = new DataView(message);
  view.setUint8(0, frameAckMessage);
  view.setBigUint64(1, BigInt(timestamp), true);
  try {
    currentSocket.send(message);
  } catch {
    currentSocket.close();
  }
}

function requestStreamResync() {
  if (resyncRequested) {
    return;
  }

  const currentSocket = socket;
  if (!currentSocket || currentSocket.readyState !== WebSocket.OPEN) {
    return;
  }

  try {
    currentSocket.send(new Uint8Array([streamResyncMessage]));
    metrics?.count('resyncRequests');
    resyncRequested = true;
  } catch {
    currentSocket.close();
  }
}
