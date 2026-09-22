import { useState } from 'react';
import { InfoCircleOutlined } from '@ant-design/icons';
import { Button, Result } from 'antd';
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
      .catch(() => {
        setState('failed');
      })
      .finally(() => {
        setIsLocked(false);
      });
  }

  if (state === 'failed') {
    return (
      <Result
        status="warning"
        title={t('settings.tailscale.failed')}
        subTitle={t('settings.tailscale.retry')}
        icon={<InfoCircleOutlined />}
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
      description={t('settings.tailscale.installDescription')}
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
