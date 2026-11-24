import { useState } from 'react';
import { Alert, Button, Result } from 'antd';
import { CirclePauseIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/zerotier.ts';
import { Panel } from '@/components/ui/settings.tsx';

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
    setErrMsg('');

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
        setErrMsg(err?.message || t('settings.zerotier.error.startFailed'));
      })
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <Panel>
      <Result
        icon={<CirclePauseIcon size={72} />}
        subTitle={t('settings.zerotier.notRunning')}
        extra={
          <Button key="run" type="primary" loading={isLoading} onClick={run}>
            {t('settings.zerotier.run')}
          </Button>
        }
      />
      {errMsg && <Alert type="error" showIcon message={errMsg} />}
    </Panel>
  );
};
