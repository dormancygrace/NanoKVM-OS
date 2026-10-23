import { useEffect, useState } from 'react';
import { Button, Result, Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';

import { confirmNetworkTrial } from '@/api/network.ts';
import { Head } from '@/components/head.tsx';

type State = 'confirming' | 'kept' | 'failed';

// A trial lasts two minutes; a page opened by hand waits at most that long.
const WAIT = 130_000;
const INTERVAL = 2000;

// What this device says about its trial: none waits, it waits with the
// change not yet applied (the interface may still be restarting), or applied.
async function trialHere() {
  try {
    const response = await fetch('/api/network/trial/probe', {
      cache: 'no-store',
      signal: AbortSignal.timeout(INTERVAL)
    });
    if (!response.ok) return null;
    const body = await response.json();
    if (body?.data?.id === '') return 'none';
    return body?.data?.applied === true ? 'applied' : 'pending';
  } catch {
    return null;
  }
}

// Confirms once the change is applied here, retrying an early or failed
// attempt until the trial ends. One run per token, also when the effect runs
// twice.
const confirmations = new Map<string, Promise<boolean>>();

function confirmOnce(token: string) {
  let run = confirmations.get(token);
  if (!run) {
    run = (async () => {
      const until = Date.now() + WAIT;
      while (Date.now() < until) {
        const state = await trialHere();
        if (state === 'none') return false;
        if (state === 'applied') {
          try {
            if ((await confirmNetworkTrial(token, INTERVAL * 2)).code === 0) return true;
          } catch {
            // Asked again in the next round.
          }
        }
        await new Promise((resolve) => setTimeout(resolve, INTERVAL));
      }
      return false;
    })();
    confirmations.set(token, run);
  }
  return run;
}

// Where the page that changed the network settings sends the browser once
// it found the device at a new address, or a link from it opens. Reaching
// this page proves the device can be reached here; no login session exists
// here yet.
export const NetworkConfirm = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [state, setState] = useState<State>('confirming');
  const token = searchParams.get('token') ?? '';

  useEffect(() => {
    let current = true;
    if (!token) {
      setState('failed');
      return;
    }
    void confirmOnce(token).then((kept) => {
      if (current) setState(kept ? 'kept' : 'failed');
    });
    return () => {
      current = false;
    };
  }, [token]);

  return (
    <>
      <Head title={t('settings.network.trial.title')} />
      <div className="flex h-screen w-screen items-center justify-center p-4">
        {state === 'confirming' ? (
          <Spin />
        ) : (
          <Result
            status={state === 'kept' ? 'success' : 'warning'}
            title={
              state === 'kept'
                ? t('settings.network.trial.confirmedHere')
                : t('settings.network.trial.confirmFailed')
            }
            extra={
              <Button type="primary" onClick={() => navigate('/', { replace: true })}>
                {t('settings.network.trial.open')}
              </Button>
            }
          />
        )}
      </div>
    </>
  );
};
