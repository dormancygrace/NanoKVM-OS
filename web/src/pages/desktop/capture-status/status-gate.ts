import type { CaptureStatus } from './model';

// A single failed encode is not a persistent display failure. Keep the last
// picture while it recovers, but show an outage even if no further status arrives.
export const TRANSIENT_CAPTURE_ERROR_DELAY_MS = 500;

export function createCaptureStatusGate(
  publish: (status: CaptureStatus | null) => void,
  schedule: (callback: () => void, delay: number) => ReturnType<typeof setTimeout> = setTimeout,
  cancel: (timer: ReturnType<typeof setTimeout>) => void = clearTimeout
) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let pending: CaptureStatus | null = null;
  let visible = false;
  const clear = () => {
    if (timer !== undefined) cancel(timer);
    timer = undefined;
    pending = null;
  };
  return {
    accept(status: CaptureStatus) {
      if (status.ok) {
        clear();
        visible = false;
        publish(null);
      } else if ((status.result === -2 || status.result === -3) && !visible) {
        pending = status;
        if (timer === undefined) {
          timer = schedule(() => {
            timer = undefined;
            visible = true;
            publish(pending);
            pending = null;
          }, TRANSIENT_CAPTURE_ERROR_DELAY_MS);
        }
      } else {
        clear();
        visible = true;
        publish(status);
      }
    },
    dispose: clear
  };
}
