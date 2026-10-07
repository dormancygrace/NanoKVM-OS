import { useState } from 'react';
import { Button, Popconfirm } from 'antd';
import { RotateCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const Reboot = () => {
  const { t } = useTranslation();

  const [isLoading, setIsLoading] = useState(false);

  function reboot() {
    if (isLoading) return;
    setIsLoading(true);

    const timeoutId = setTimeout(() => {
      window.location.reload();
    }, 30000);

    api
      .reboot()
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          setIsLoading(false);
          clearTimeout(timeoutId);
        }
      })
      .catch((err) => {
        showRequestError(err);
        setIsLoading(false);
        clearTimeout(timeoutId);
      });
  }

  return (
    <SettingRow label={t('settings.device.reboot')}>
      <Popconfirm
        placement="bottomRight"
        title={t('settings.device.rebootDesc')}
        okText={t('settings.device.okBtn')}
        cancelText={t('settings.device.cancelBtn')}
        onConfirm={reboot}
      >
        <Button danger loading={isLoading} icon={<RotateCwIcon size={16} />}>
          {t('settings.device.reboot')}
        </Button>
      </Popconfirm>
    </SettingRow>
  );
};
