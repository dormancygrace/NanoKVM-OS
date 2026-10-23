import { useEffect, useRef, useState } from 'react';
import { Button, Result } from 'antd';
import { isAxiosError } from 'axios';
import { CircleAlertIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';

import { ExtensionInstallResult } from '../vpn/extension-install-result.tsx';

type InstallProps = {
  setIsLocked: (setIsLocked: boolean) => void;
  onSuccess: () => void;
};

export const Install = ({ setIsLocked, onSuccess }: InstallProps) => {
  const { t } = useTranslation();

  const [state, setState] = useState('');
  // The wait for an install that outlived its request ends with the panel.
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  function install() {
    if (state === 'installing') return;
    setState('installing');
    setIsLocked(true);

    api
      .install()
      .then((rsp) => {
        if (rsp.code !== 0) {
          setState('failed');
          return;
        }

        onSuccess();
        setState('');
      })
      .catch(async (err) => {
        // No answer is not a failure: the install may still be running on
        // the device. Wait for it to finish, then show what it did.
        const installed = isAxiosError(err) && !err.response && (await installFinished());
        if (!mounted.current) return;
        if (installed) {
          onSuccess();
          setState('');
          return;
        }
        setState('failed');
      })
      .finally(() => {
        setIsLocked(false);
      });
  }

  // Polls the status while the server reports an operation in progress;
  // true when Tailscale is installed at the end.
  async function installFinished() {
    const deadline = Date.now() + 15 * 60 * 1000;
    while (Date.now() < deadline && mounted.current) {
      await new Promise((resolve) => setTimeout(resolve, 3000));
      try {
        const rsp = await api.getStatus();
        if (rsp.code !== 0) continue;
        if (rsp.data?.busy) continue;
        return rsp.data?.state !== 'notInstall';
      } catch {
        /* the device may be busy; ask again */
      }
    }
    return false;
  }

  if (state === 'failed') {
    return (
      <Result
        status="warning"
        title={t('settings.tailscale.failed')}
        subTitle={t('settings.tailscale.retry')}
        icon={<CircleAlertIcon size={72} />}
        extra={
          <Button type="primary" onClick={install}>
            {t('settings.tailscale.install')}
          </Button>
        }
      />
    );
  }

  return (
    <ExtensionInstallResult
      title={t('settings.tailscale.notInstall')}
      description={`${t('settings.tailscale.installDescription')} ${t('settings.software.community.vpnNote')}`}
      actionLabel={
        state === 'installing'
          ? t('settings.tailscale.installing')
          : t('settings.tailscale.install')
      }
      loading={state === 'installing'}
      onInstall={install}
    />
  );
};
