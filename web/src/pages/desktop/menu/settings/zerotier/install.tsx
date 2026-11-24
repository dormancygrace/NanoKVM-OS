import { useState } from 'react';
import { Alert } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/zerotier.ts';

import { ExtensionInstallResult } from '../vpn/extension-install-result.tsx';

type InstallProps = {
  setIsLocked: (isLocked: boolean) => void;
  onSuccess: () => void;
};

export const Install = ({ setIsLocked, onSuccess }: InstallProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [errMsg, setErrMsg] = useState('');

  function install() {
    if (isLoading) return;
    setIsLocked(true);
    setIsLoading(true);
    setErrMsg('');

    api
      .install()
      .then((rsp) => {
        if (rsp.code !== 0) {
          setErrMsg(rsp.msg);
          return;
        }
        onSuccess();
      })
      .catch((err) => {
        setErrMsg(err?.message || t('settings.zerotier.error.installFailed'));
      })
      .finally(() => {
        setIsLoading(false);
        setIsLocked(false);
      });
  }

  return (
    <>
      <ExtensionInstallResult
        title={t('settings.zerotier.notInstall')}
        description={t('settings.zerotier.installDescription')}
        actionLabel={isLoading ? t('settings.zerotier.installing') : t('settings.zerotier.install')}
        loading={isLoading}
        onInstall={install}
      />

      {errMsg && <Alert type="error" showIcon message={errMsg} />}
    </>
  );
};
