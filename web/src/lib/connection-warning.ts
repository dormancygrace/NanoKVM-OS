// When to show that the input connection is lost. A browser may cut the
// sockets of a background tab and the client reconnects at once on return
// (lib/websocket.ts), usually within a handshake: a warning shows only when
// the connection is still lost after a second of the tab being visible,
// counted from the later of the loss and the return, and never while the tab
// is hidden.

type Env = {
  isVisible: () => boolean;
  later: (callback: () => void, ms: number) => () => void;
};

export const CONNECTION_WARNING_DELAY = 1000;

export function createConnectionWarningGate(
  onQuietChange: (quiet: boolean) => void,
  env: Env,
  delay = CONNECTION_WARNING_DELAY
) {
  let quiet = false;
  let cancel: (() => void) | undefined;

  const set = (value: boolean) => {
    if (quiet === value) return;
    quiet = value;
    onQuietChange(value);
  };
  // Quiet now; loud again after a visible second. A hidden tab stays quiet
  // until its return starts the second again.
  const hush = () => {
    cancel?.();
    cancel = undefined;
    set(true);
    if (!env.isVisible()) return;
    cancel = env.later(() => {
      cancel = undefined;
      if (env.isVisible()) set(false);
    }, delay);
  };

  return {
    isQuiet: () => quiet,
    // The connection was lost (call before paint).
    lost: hush,
    // The tab was hidden or shown.
    visibilityChanged: hush,
    dispose() {
      cancel?.();
      cancel = undefined;
    }
  };
}
