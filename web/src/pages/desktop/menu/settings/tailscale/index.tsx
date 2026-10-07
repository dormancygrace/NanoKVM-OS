import { useCallback, useEffect, useRef, useState } from 'react';
import { LoaderCircleIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';

import { Device } from './device.tsx';
import { Header } from './header.tsx';
import { Install } from './install.tsx';
import { Login } from './login.tsx';
import { Run } from './run.tsx';
import type { Status } from './types.ts';

// Stored as a key and translated on render, so getStatus stays stable across
// language changes and does not refetch.
const statusFailed = 'settings.tailscale.statusFailed';

type TailscaleProps = {
  setIsLocked: (isLocked: boolean) => void;
};

export const Tailscale = ({ setIsLocked }: TailscaleProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [status, setStatus] = useState<Status>();
  const [errMsg, setErrMsg] = useState('');

  const statusInFlight = useRef(false);
  const getStatus = useCallback(() => {
    if (statusInFlight.current) return;
    statusInFlight.current = true;
    setIsLoading(true);

    api
      .getStatus()
      .then((rsp) => {
        if (rsp.code !== 0) {
          setErrMsg(rsp.msg);
          return;
        }

        setStatus(rsp.data);
      })
      .catch((err) => {
        setErrMsg(err?.message || statusFailed);
      })
      .finally(() => {
        statusInFlight.current = false;
        setIsLoading(false);
      });
  }, []);

  useEffect(() => {
    getStatus();
  }, [getStatus]);

  return (
    <>
      <Header state={status?.state} onSuccess={getStatus} />

      {isLoading ? (
        <div className="text-fg-muted flex w-full items-center justify-center space-x-2 pt-5">
          <LoaderCircleIcon className="animate-spin" size={18} />
          <span>{t('settings.tailscale.loading')}</span>
        </div>
      ) : (
        <>
          {status?.state === 'notInstall' && (
            <Install setIsLocked={setIsLocked} onSuccess={getStatus} />
          )}

          {status?.state === 'notRunning' && <Run onSuccess={getStatus} />}

          {status?.state === 'notLogin' && <Login onSuccess={getStatus} />}

          {(status?.state === 'stopped' || status?.state === 'running') && (
            <Device status={status} onLogout={getStatus} />
          )}

          {errMsg && (
            <div className="pt-5 text-red-500">
              {errMsg === statusFailed ? t(statusFailed) : errMsg}
            </div>
          )}
        </>
      )}
    </>
  );
};
