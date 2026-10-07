import { browserPollEnv, pollWhileVisible, type PollEnv } from '@/lib/visible-poll.ts';

// GET /api/vm/live-status: each part has the payload of its own endpoint
// (vm/hdmi, vm/gpio, hid/input-status, stream/audio/status, vm/device/virtual).
// usb is null for non-administrators; gpio is null when the LEDs cannot be read.
export type LiveStatus = {
  hdmi: { enabled: boolean; viewerCount: number; signal: boolean; idleTimeout: number };
  gpio: { pwr: boolean; hdd: boolean } | null;
  input: unknown;
  audio: { audio: boolean };
  usb: { disk?: boolean; mode?: string } | null;
};

// status is null when the request failed. startedAt (performance.now() when
// the request was sent) lets a listener drop data older than its own change.
export type LiveStatusListener = (status: LiveStatus | null, startedAt: number) => void;

export const liveStatusInterval = 3000;

// One poll shared by every listener on the page instead of one per widget.
export function createLiveStatus(
  read: () => Promise<LiveStatus | null>,
  env: PollEnv = browserPollEnv,
  now: () => number = () => performance.now()
) {
  const listeners = new Set<LiveStatusListener>();
  let last: { status: LiveStatus | null; startedAt: number } | undefined;
  let inFlight: Promise<void> | undefined;
  let again = false;
  let stopPolling: (() => void) | undefined;

  async function run() {
    do {
      again = false;
      const startedAt = now();
      let status: LiveStatus | null;
      try {
        status = await read();
      } catch {
        status = null;
      }
      last = { status, startedAt };
      for (const listener of [...listeners]) listener(status, startedAt);
    } while (again);
    inFlight = undefined;
  }

  // force: the state just changed, so a request already on its way may be
  // stale; read once more after it.
  function refresh({ force = false } = {}) {
    if (inFlight) {
      if (force) again = true;
      return inFlight;
    }
    inFlight = run();
    return inFlight;
  }

  function subscribe(listener: LiveStatusListener) {
    listeners.add(listener);
    if (last) listener(last.status, last.startedAt);
    else void refresh();
    stopPolling ??= pollWhileVisible(() => void refresh(), liveStatusInterval, env);
    return () => {
      listeners.delete(listener);
      if (listeners.size === 0) {
        stopPolling?.();
        stopPolling = undefined;
        last = undefined;
      }
    };
  }

  return { subscribe, refresh };
}
