// Adapted from IronKVM 0139a98c (GPL-3.0): passive API polling only.
export type PollEnv = {
  isVisible: () => boolean;
  onVisibilityChange: (callback: () => void) => () => void;
  setInterval: (callback: () => void, ms: number) => () => void;
};

export const browserPollEnv: PollEnv = {
  isVisible: () => document.visibilityState === 'visible',
  onVisibilityChange: (callback) => {
    document.addEventListener('visibilitychange', callback);
    return () => document.removeEventListener('visibilitychange', callback);
  },
  setInterval: (callback, ms) => {
    const timer = window.setInterval(callback, ms);
    return () => window.clearInterval(timer);
  }
};

// Existing callers own their initial read. Returning to a visible tab refreshes
// immediately. Heartbeats, transfers and user actions keep their own lifecycle.
export function pollWhileVisible(fn: () => void, ms: number, env: PollEnv = browserPollEnv) {
  let stopped = false;
  const tick = () => {
    if (!stopped && env.isVisible()) fn();
  };
  const stopTimer = env.setInterval(tick, ms);
  const stopListening = env.onVisibilityChange(tick);
  return () => {
    if (stopped) return;
    stopped = true;
    stopTimer();
    stopListening();
  };
}
