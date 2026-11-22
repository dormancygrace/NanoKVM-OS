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
  // One status request at a time: a slow answer is not dropped by the next
  // tick. A refresh asked for while one is on its way (after an action) reads
  // once more after it; a poll tick is skipped. generation drops the answers
  // that arrive after unmount.
  const generation = useRef(0);
  const inFlight = useRef(false);
  const again = useRef(false);

  const read = useCallback(async () => {
    const current = generation.current;
    inFlight.current = true;
    setIsLoading(true);
    try {
      do {
        again.current = false;
        try {
          const rsp = await api.getStatus();
          if (current !== generation.current) return;
          if (rsp.code !== 0) {
            setErrMsg(rsp.msg);
          } else {
            setErrMsg('');
            setStatus(rsp.data);
          }
        } catch (err: any) {
          if (current !== generation.current) return;
          setErrMsg(err?.message || statusFailed);
        }
      } while (again.current);
    } finally {
      if (current === generation.current) {
        inFlight.current = false;
        setIsLoading(false);
      }
    }
  }, []);

  const getStatus = useCallback(() => {
    if (inFlight.current) {
      again.current = true;
      return;
    }
    void read();
  }, [read]);

  useEffect(() => {
    getStatus();
    // A joined network moves from requesting its configuration to OK or
    // access denied when the controller answers; follow it while visible.
    const stopPolling = pollWhileVisible(() => {
      if (!inFlight.current) void read();
    }, 5000);

    return () => {
      // Effects are mounted twice in development Strict Mode: the remount
      // starts afresh instead of waiting for the dropped request.
      generation.current += 1;
      inFlight.current = false;
      again.current = false;
      stopPolling();
    };
  }, [getStatus, read]);

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
