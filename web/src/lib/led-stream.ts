import { browserPollEnv } from './visible-poll.ts';

export type LEDStatus = { pwr: boolean; hdd: boolean } | null;
type Listener = (status: LEDStatus) => void;
export type LEDSource = Pick<EventSource, 'onmessage' | 'onerror' | 'close'>;

type Env = {
  isVisible: () => boolean;
  onVisibilityChange: (callback: () => void) => () => void;
  later: (callback: () => void, ms: number) => () => void;
  now: () => number;
};
const browserEnv: Env = {
  ...browserPollEnv,
  later: (callback, ms) => {
    const timer = window.setTimeout(callback, ms);
    return () => window.clearTimeout(timer);
  },
  now: () => performance.now()
};

// One connection per visible desktop, independent of the power popover. The
// existing shared poll provides a fallback while SSE is unavailable.
export function createLEDStream(
  connect: () => LEDSource,
  fallback: (listener: Listener) => () => void,
  env: Env = browserEnv
) {
  const listeners = new Set<Listener>();
  let last: LEDStatus = null;
  let source: LEDSource | undefined;
  let stopFallback: (() => void) | undefined;
  let stopVisibility: (() => void) | undefined;
  let stopRetry: (() => void) | undefined;
  let stopClear: (() => void) | undefined;
  let hddUntil = 0;
  let generation = 0;

  function emit(status: LEDStatus) {
    last = status;
    for (const listener of [...listeners]) listener(status);
  }

  function display(status: LEDStatus) {
    stopClear?.();
    stopClear = undefined;
    if (!status) {
      hddUntil = 0;
      emit(null);
      return;
    }
    if (status.hdd) hddUntil = env.now() + 40;
    // A short on/off pair can arrive in one network read. Keep that real
    // activity visible for about 40 ms, without fake pulsing.
    const remaining = hddUntil - env.now();
    if (!status.hdd && remaining > 0) {
      emit({ ...status, hdd: true });
      stopClear = env.later(() => {
        stopClear = undefined;
        emit(status);
      }, Math.ceil(remaining));
    } else {
      emit(status);
    }
  }

  function stop() {
    generation++;
    source?.close();
    source = undefined;
    stopRetry?.();
    stopRetry = undefined;
    stopFallback?.();
    stopFallback = undefined;
    display(null);
  }

  function start() {
    if (listeners.size === 0 || !env.isVisible()) return;
    const current = ++generation;
    const valid = () => current === generation;
    let sourceReady = false;
    stopFallback ??= fallback((status) => {
      if (valid() && env.isVisible() && !sourceReady) display(status);
    });
    const failed = () => {
      if (!valid()) return;
      stop();
      if (listeners.size === 0 || !env.isVisible()) return;
      const fallbackGeneration = generation;
      stopFallback = fallback((status) => {
        if (generation === fallbackGeneration && env.isVisible()) display(status);
      });
      stopRetry = env.later(() => {
        stopRetry = undefined;
        stopFallback?.();
        stopFallback = undefined;
        start();
      }, 3000);
    };
    try {
      const stream = connect();
      source = stream;
      stream.onmessage = (event) => {
        if (!valid()) return;
        let status: LEDStatus;
        try {
          status = JSON.parse(event.data) as LEDStatus;
          if (
            status !== null &&
            (typeof status?.pwr !== 'boolean' || typeof status?.hdd !== 'boolean')
          ) {
            failed();
            return;
          }
        } catch {
          failed();
          return;
        }
        sourceReady = true;
        stopFallback?.();
        stopFallback = undefined;
        display(status);
      };
      stream.onerror = failed;
    } catch {
      failed();
    }
  }

  function subscribe(listener: Listener) {
    listeners.add(listener);
    listener(last);
    if (listeners.size === 1) {
      stopVisibility = env.onVisibilityChange(() => {
        stop();
        start();
      });
      start();
    }
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) {
        stopVisibility?.();
        stopVisibility = undefined;
        stop();
      }
    };
  }
  return { subscribe };
}
