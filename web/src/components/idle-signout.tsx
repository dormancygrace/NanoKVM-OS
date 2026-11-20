import { useEffect, useState } from 'react';
import { Alert, message } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/auth.ts';
import { AUTH_EXPIRED_EVENT, IDLE_CHANGED_EVENT } from '@/lib/auth-events.ts';

// Sign-out after inactivity. The device keeps the clock of this sign-in and
// ends it; the page tells it of input (not of its own requests, which go on
// for as long as it is open), and shows the last minute counting down.
//
// Input within REPORT_EVERY of the last report is reported when that time
// is up, so the device's clock is never more than that behind.
const REPORT_EVERY = 20_000;
// With no limit set, an active page still asks now and then: an
// administrator may set one meanwhile.
const ASK_EVERY = 60_000;
const WARN_BEFORE = 60_000;
// In the last minute, how often to ask whether another tab of this sign-in
// has been active.
const RECHECK_EVERY = 10_000;

const INPUTS = ['pointerdown', 'pointermove', 'keydown', 'wheel', 'touchstart'] as const;

export const IdleSignout = () => {
  const { t } = useTranslation();
  const [secondsLeft, setSecondsLeft] = useState<number | null>(null);

  useEffect(() => {
    let stopped = false;
    let limit = 0;
    // When the device's period ends, by this page's clock.
    let deadline = 0;
    let lastAsked = 0;
    let asking = false;
    // Input seen while a request was out: reported when it is back.
    let activityPending = false;
    let trailing: ReturnType<typeof setTimeout> | undefined;
    let ticker: ReturnType<typeof setInterval> | undefined;

    const show = () => {
      const left = deadline - Date.now();
      setSecondsLeft(limit > 0 && left <= WARN_BEFORE ? Math.max(0, Math.ceil(left / 1000)) : null);
    };

    const ask = (activity: boolean) => {
      if (asking) {
        if (activity) activityPending = true;
        return;
      }
      asking = true;
      lastAsked = Date.now();
      (activity ? api.reportActivity() : api.getIdle())
        .then((rsp) => {
          if (stopped || rsp.code !== 0 || !rsp.data) return;
          limit = rsp.data.available ? rsp.data.limit * 1000 : 0;
          deadline = Date.now() + rsp.data.left * 1000;
          if (limit > 0 && !ticker) ticker = setInterval(check, 1000);
          if (limit === 0 && ticker) {
            clearInterval(ticker);
            ticker = undefined;
          }
          show();
        })
        // Refused: the sign-in is over, and onExpired has said why.
        .catch(() => {})
        .finally(() => {
          asking = false;
          if (activityPending && !stopped) {
            activityPending = false;
            ask(true);
          }
        });
    };

    function check() {
      if (limit === 0) return;
      const now = Date.now();
      const left = deadline - now;
      show();
      if (left <= 0 || (left <= WARN_BEFORE && now - lastAsked >= RECHECK_EVERY)) ask(false);
    }

    const onInput = () => {
      const now = Date.now();
      if (limit === 0) {
        if (now - lastAsked >= ASK_EVERY) ask(false);
        return;
      }
      const since = now - lastAsked;
      if (since >= REPORT_EVERY || deadline - now <= WARN_BEFORE) {
        ask(true);
      } else if (!trailing) {
        trailing = setTimeout(() => {
          trailing = undefined;
          ask(true);
        }, REPORT_EVERY - since);
      }
    };

    const onVisible = () => {
      if (document.visibilityState === 'visible') check();
    };
    const onChanged = () => ask(false);
    // Whichever request met the refusal first (this watch's, the page's own,
    // a closed connection): when the period was out, say that this is why
    // the sign-in form opens.
    const onExpired = () => {
      if (limit > 0 && Date.now() >= deadline - 5000) {
        // One notice, however many requests were refused.
        message.info({ content: t('auth.idleSignedOut'), duration: 10, key: 'idle-signout' });
      }
    };

    // In the capture phase: the remote screen keeps its input to itself.
    for (const name of INPUTS) {
      window.addEventListener(name, onInput, { capture: true, passive: true });
    }
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener(IDLE_CHANGED_EVENT, onChanged);
    window.addEventListener(AUTH_EXPIRED_EVENT, onExpired);
    ask(false);

    return () => {
      stopped = true;
      for (const name of INPUTS) window.removeEventListener(name, onInput, { capture: true });
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener(IDLE_CHANGED_EVENT, onChanged);
      window.removeEventListener(AUTH_EXPIRED_EVENT, onExpired);
      clearTimeout(trailing);
      clearInterval(ticker);
    };
  }, [t]);

  if (secondsLeft === null) return null;
  const time = `${Math.floor(secondsLeft / 60)}:${String(secondsLeft % 60).padStart(2, '0')}`;
  return (
    <div className="pointer-events-none fixed inset-x-0 top-3 z-[2000] flex justify-center px-4">
      <Alert
        type="warning"
        showIcon
        role="alert"
        message={t('auth.idleWarning', { time })}
        className="shadow-lg"
      />
    </div>
  );
};
