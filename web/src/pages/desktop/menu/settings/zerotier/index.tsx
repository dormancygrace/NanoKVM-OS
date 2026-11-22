import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert } from 'antd';
import { LoaderCircleIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/zerotier.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';

import { Device } from './device.tsx';
import { Header } from './header.tsx';
import { Install } from './install.tsx';
import { Run } from './run.tsx';

// Stored as a key and translated on render, so getStatus stays stable across
// language changes and does not refetch.
const statusFailed = 'settings.zerotier.error.statusFailed';

type ZerotierProps = {
  setIsLocked: (isLocked: boolean) => void;
};

export const Zerotier = ({ setIsLocked }: ZerotierProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [status, setStatus] = useState<api.ZerotierStatus>();
  const [errMsg, setErrMsg] = useState('');
  const errorText = errMsg === statusFailed ? t(statusFailed) : errMsg;
  const requestId = useRef(0);
  const isMounted = useRef(true);

  const getStatus = useCallback(async () => {
    const currentRequestId = ++requestId.current;
    setIsLoading(true);
    try {
      const rsp = await api.getStatus();
      if (!isMounted.current || currentRequestId !== requestId.current) return;
      if (rsp.code !== 0) {
        setErrMsg(rsp.msg);
        return;
      }
      setErrMsg('');
      setStatus(rsp.data);
    } catch (err: any) {
      if (!isMounted.current || currentRequestId !== requestId.current) return;
      setErrMsg(err?.message || statusFailed);
    } finally {
      if (isMounted.current && currentRequestId === requestId.current) {
        setIsLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    // Effects are mounted twice in development Strict Mode. Restore the guard
    // here rather than allowing the first cleanup to suppress the real request.
    isMounted.current = true;
    void getStatus();
    // A joined network moves from requesting its configuration to OK or
    // access denied when the controller answers; follow it while visible.
    const stopPolling = pollWhileVisible(() => void getStatus(), 5000);

    return () => {
      isMounted.current = false;
      requestId.current += 1;
      stopPolling();
    };
  }, [getStatus]);

  const running = status?.state === 'running' || status?.state === 'connecting';

  return (
    <div className="space-y-6">
      <Header state={status?.state} onSuccess={getStatus} />

      {isLoading && !status ? (
        <div className="text-fg-muted flex w-full items-center justify-center gap-2">
          <LoaderCircleIcon className="animate-spin" size={16} />
          <span>{t('settings.zerotier.loading')}</span>
        </div>
      ) : (
        <>
          {errMsg && (
            <Alert
              type="warning"
              showIcon
              message={t(
                status ? 'settings.zerotier.statusStale' : 'settings.zerotier.statusUnknown'
              )}
              description={errorText}
            />
          )}

          {status?.state === 'notInstall' && (
            <Install setIsLocked={setIsLocked} onSuccess={getStatus} />
          )}

          {status?.state === 'notRunning' && <Run onSuccess={getStatus} />}

          {running && status && <Device status={status} onChange={getStatus} />}
        </>
      )}
    </div>
  );
};
