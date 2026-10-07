import { useState } from 'react';
import { Button, Card, Result } from 'antd';
import { CirclePauseIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';

type RunProps = {
  onSuccess: () => void;
};

export const Run = ({ onSuccess }: RunProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);
  const [errMsg, setErrMsg] = useState('');

  function run() {
    if (isLoading) return;
    setIsLoading(true);

    api
      .start()
      .then((rsp) => {
        if (rsp.code !== 0) {
          setErrMsg(rsp.msg);
          return;
        }

        onSuccess();
      })
      .catch((err) => {
        setErrMsg(err?.message || t('settings.tailscale.startFailed'));
      })
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <Card>
      <Result
        icon={<CirclePauseIcon size={72} />}
        subTitle={t('settings.tailscale.notRunning')}
        extra={
          <Button key="install" type="primary" loading={isLoading} onClick={run}>
            {t('settings.tailscale.run')}
          </Button>
        }
      />

      <div className="flex justify-center">
        {errMsg && <span className="text-red-500">{errMsg}</span>}
      </div>
    </Card>
  );
};
