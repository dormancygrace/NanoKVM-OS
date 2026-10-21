// The output belongs to the listening preference, not to a network connection.
// Keep an unlocked AudioContext across reconnects and transport changes.
export function createAudioOutput(onBlocked: (blocked: boolean) => void) {
  const context = new AudioContext({ latencyHint: 'interactive' });
  const gain = context.createGain();
  gain.connect(context.destination);
  let disposed = false;
  let lastState = context.state;

  const report = () => {
    if (!disposed) onBlocked(context.state !== 'running');
  };
  const resume = async () => {
    if (disposed || context.state === 'closed') return;
    // Call synchronously: a user activation must still be in scope here.
    const pending = context.resume();
    report();
    try {
      await pending;
    } finally {
      report();
    }
  };
  const recover = () => {
    if (!disposed && context.state !== 'running') void resume().catch(() => {});
  };
  const visible = () => {
    if (document.visibilityState === 'visible') recover();
  };
  const stateChanged = () => {
    const previous = lastState;
    lastState = context.state;
    report();
    // OS audio interruptions can suspend an already authorized context.
    if (previous === 'running') recover();
  };
  // Capture also sees interactions consumed by the remote keyboard/mouse.
  // pointerup covers touch activation, keydown covers keyboard-only use.
  const gestures = ['pointerdown', 'pointerup', 'click', 'keydown'] as const;
  gestures.forEach((event) => window.addEventListener(event, recover, true));
  window.addEventListener('focus', recover);
  window.addEventListener('pageshow', recover);
  document.addEventListener('visibilitychange', visible);
  context.addEventListener('statechange', stateChanged);
  return {
    context,
    gain,
    resume,
    close() {
      if (disposed) return;
      disposed = true;
      gestures.forEach((event) => window.removeEventListener(event, recover, true));
      window.removeEventListener('focus', recover);
      window.removeEventListener('pageshow', recover);
      document.removeEventListener('visibilitychange', visible);
      context.removeEventListener('statechange', stateChanged);
      gain.disconnect();
      void context.close().catch(() => {});
    }
  };
}
