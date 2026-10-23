import { useEffect, useState } from 'react';
import { Button, Result, Spin } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';

import { confirmNetworkTrial } from '@/api/network.ts';
import { Head } from '@/components/head.tsx';

type State = 'confirming' | 'kept' | 'failed';

// One request per token, also when the effect runs twice.
const confirmations = new Map<string, Promise<boolean>>();

function confirmOnce(token: string) {
  let pending = confirmations.get(token);
  if (!pending) {
    pending = confirmNetworkTrial(token)
      .then((rsp) => rsp.code === 0)
      .catch(() => false);
    confirmations.set(token, pending);
  }
  return pending;
}

// Where the page that changed the network settings sends the browser once
// it found the device at a new address. Reaching this page proves the
// device can be reached here; no login session exists here yet.
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
