import { Popconfirm } from 'antd';
import { useAtomValue } from 'jotai';
import { PowerIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { themeAtom } from '@/jotai/settings.ts';

import { reportGpioResult } from './report';

type PowerShortProps = {
  isLoading: boolean;
  setIsLoading: (loading: boolean) => void;
};

export const PowerShort = ({ isLoading, setIsLoading }: PowerShortProps) => {
  const { t } = useTranslation();
  const dark = useAtomValue(themeAtom) === 'dark';

  function power() {
    if (isLoading) return;
    setIsLoading(true);

    api
      .setGpio('power', 800)
      .then((rsp) => reportGpioResult(rsp, t))
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <>
      <Popconfirm
        placement="bottomLeft"
        title={t('power.powerConfirm')}
        okText={t('power.okBtn')}
        cancelText={t('power.cancelBtn')}
        onConfirm={power}
        color={dark ? '#404040' : undefined}
      >
        <div className="flex cursor-pointer items-center space-x-2 rounded px-3 py-1.5 select-none hover:bg-neutral-700/70">
          <PowerIcon size={16} />
          <span>{t('power.powerShort')}</span>
        </div>
      </Popconfirm>
    </>
  );
};
