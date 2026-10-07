import { useState } from 'react';
import { Button, Result } from 'antd';
import { CirclePauseIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { Panel } from '@/components/ui/settings.tsx';

type RunProps = {
  onSuccess: () => void;
};

export const Run = ({ onSuccess }: RunProps) => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);

  function run() {
    if (isLoading) return;
    setIsLoading(true);

    api
      .start()
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp, 'settings.tailscale.startFailed');
          return;
        }

        onSuccess();
      })
      .catch((err) => showRequestError(err, 'settings.tailscale.startFailed'))
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <Panel>
      <Result
        icon={<CirclePauseIcon size={72} />}
        subTitle={t('settings.tailscale.notRunning')}
        extra={
          <Button key="install" type="primary" loading={isLoading} onClick={run}>
            {t('settings.tailscale.run')}
          </Button>
        }
      />
    </Panel>
  );
};
