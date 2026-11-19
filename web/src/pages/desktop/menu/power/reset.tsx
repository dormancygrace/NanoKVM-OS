import { Popconfirm } from 'antd';
import { useAtomValue } from 'jotai';
import { RotateCcwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { setGpio } from '@/api/vm.ts';
import { themeAtom } from '@/jotai/settings.ts';

import { reportGpioResult } from './report';

type ResetProps = {
  showConfirm: boolean;
  isLoading: boolean;
  setIsLoading: (loading: boolean) => void;
};

export const Reset = ({ showConfirm, isLoading, setIsLoading }: ResetProps) => {
  const { t } = useTranslation();
  const dark = useAtomValue(themeAtom) === 'dark';

  function reset() {
    if (isLoading) return;
    setIsLoading(true);

    setGpio('reset', 800)
      .then((rsp) => reportGpioResult(rsp, t))
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <>
      {showConfirm ? (
        <Popconfirm
          placement="bottomLeft"
          title={t('power.resetConfirm')}
          okText={t('power.okBtn')}
          cancelText={t('power.cancelBtn')}
          onConfirm={reset}
          color={dark ? '#404040' : undefined}
        >
          <div className="flex cursor-pointer items-center space-x-2 rounded px-3 py-1.5 select-none hover:bg-neutral-700/70">
            <RotateCcwIcon size={16} />
            <span>{t('power.reset')}</span>
          </div>
        </Popconfirm>
      ) : (
        <div
          className="flex cursor-pointer items-center space-x-2 rounded px-3 py-1.5 select-none hover:bg-neutral-700/70"
          onClick={reset}
        >
          <RotateCcwIcon size={16} />
          <span>{t('power.reset')}</span>
        </div>
      )}
    </>
  );
};
