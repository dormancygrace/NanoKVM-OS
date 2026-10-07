import { useState } from 'react';
import { Button } from 'antd';
import { LogInIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { Panel } from '@/components/ui/settings.tsx';

type LoginProps = {
  onSuccess: () => void;
};

export const Login = ({ onSuccess }: LoginProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [loginUrl, setLoginUrl] = useState('');

  function login() {
    if (isLoading) return;
    setIsLoading(true);

    api
      .login()
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp, 'settings.tailscale.loginFailed');
          return;
        }

        const url = rsp.data.url;
        if (!url) {
          onSuccess();
          return;
        }

        setLoginUrl(url);
        window.open(url, '_blank');
        setTimeout(() => setLoginUrl(''), 10 * 60 * 1000);
      })
      .catch((err) => showRequestError(err, 'settings.tailscale.loginFailed'))
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <div className="flex flex-col items-center justify-center space-y-6">
      <Panel>{t('settings.tailscale.notLogin')}</Panel>

      {loginUrl === '' ? (
        <Button
          type="primary"
          size="large"
          shape="round"
          icon={<LogInIcon size={16} />}
          loading={isLoading}
          onClick={login}
        >
          {t('settings.tailscale.login')}
        </Button>
      ) : (
        <div className="flex w-full flex-col items-center justify-center space-y-4">
          <Button type="link" href={loginUrl} target="_blank">
            {loginUrl}
          </Button>

          <span className="text-fg-muted text-xs">{t('settings.tailscale.urlPeriod')}</span>

          <Button type="primary" size="large" shape="round" onClick={onSuccess}>
            {t('settings.tailscale.loginSuccess')}
          </Button>
        </div>
      )}
    </div>
  );
};
