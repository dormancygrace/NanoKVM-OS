import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

// Anchor to the device clock, then advance locally without polling every second.
export function HandshakeAge({ timestamp, serverNow }: { timestamp: number; serverNow?: number }) {
  const { i18n } = useTranslation();
  const [sample, setSample] = useState(() => ({
    now: serverNow ?? Date.now(),
    at: performance.now()
  }));
  const [elapsed, setElapsed] = useState(0);
  useEffect(() => {
    setSample({ now: serverNow ?? Date.now(), at: performance.now() });
    setElapsed(0);
  }, [timestamp, serverNow]);
  useEffect(() => {
    const timer = window.setInterval(() => setElapsed(performance.now() - sample.at), 1000);
    return () => window.clearInterval(timer);
  }, [sample]);
  const seconds = Math.max(0, Math.floor((sample.now + elapsed) / 1000 - timestamp));
  const unit =
    seconds < 60 ? 'second' : seconds < 3600 ? 'minute' : seconds < 86400 ? 'hour' : 'day';
  const divisor = { second: 1, minute: 60, hour: 3600, day: 86400 }[unit];
  return (
    <>
      {new Intl.RelativeTimeFormat(i18n.language, { numeric: 'always' }).format(
        -Math.floor(seconds / divisor),
        unit
      )}
    </>
  );
}
