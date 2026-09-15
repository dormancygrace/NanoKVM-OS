import { useEffect, useState } from 'react';
import { Divider, Switch, Tooltip } from 'antd';
import clsx from 'clsx';
import { HardDriveIcon, LoaderCircleIcon, PowerIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm';
import * as localstorage from '@/lib/localstorage.ts';
import { MenuItem } from '@/components/menu-item.tsx';

import { PowerLong } from './power-long.tsx';
import { PowerShort } from './power-short.tsx';
import { Reset } from './reset.tsx';

export const Power = () => {
  const { t } = useTranslation();

  const [isPowerOn, setIsPowerOn] = useState(false);
  const [isHddActive, setIsHddActive] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [showConfirm, setShowConfirm] = useState(localstorage.getPowerConfirm);

  useEffect(() => {
    let disposed = false;
    let inFlight = false;

    async function refreshLeds() {
      if (inFlight) return;
      inFlight = true;
      try {
        const rsp = await api.getGpio();
        if (!disposed && rsp.code === 0) {
          setIsPowerOn(rsp.data.pwr);
          setIsHddActive(rsp.data.hdd);
        }
      } catch (err) {
        console.log(err);
      } finally {
        inFlight = false;
      }
    }

    void refreshLeds();
    const interval = window.setInterval(refreshLeds, 200);

    return () => {
      disposed = true;
      window.clearInterval(interval);
    };
  }, []);

  function updateShowConfirm(value: boolean) {
    setShowConfirm(value);
    localstorage.setPowerConfirm(value);
  }

  const icon = (
    <div
      className={clsx(
        'h-[18px] w-[18px]',
        isPowerOn
          ? 'text-green-500 drop-shadow-[0_0_4px_currentColor]'
          : 'text-neutral-300 hover:text-white'
      )}
    >
      {isLoading ? (
        <LoaderCircleIcon className="animate-spin" size={18} />
      ) : (
        <PowerIcon size={18} />
      )}
    </div>
  );

  const content = (
    <div className="min-w-[200px]">
      <div className="flex items-center justify-between px-1">
        <span className="text-base font-bold text-neutral-300">{t('power.title')}</span>

        <div className="flex items-center space-x-2">
          <Tooltip title={t('power.showConfirmTip')} placement="right">
            <div className="flex items-center space-x-2">
              <span className="text-xs text-neutral-400">{t('power.showConfirm')}</span>

              <Switch size="small" checked={showConfirm} onChange={updateShowConfirm} />
            </div>
          </Tooltip>
        </div>
      </div>

      <Divider style={{ margin: '10px 0 15px 0' }} />

      <div className="flex flex-col space-y-1">
        <Reset showConfirm={showConfirm} isLoading={isLoading} setIsLoading={setIsLoading} />
        <PowerShort showConfirm={showConfirm} isLoading={isLoading} setIsLoading={setIsLoading} />
        <PowerLong showConfirm={showConfirm} isLoading={isLoading} setIsLoading={setIsLoading} />
      </div>
    </div>
  );

  return (
    <div className="flex shrink-0 items-center">
      <MenuItem title={t('power.title')} icon={icon} content={content} />
      <Tooltip title="HDD LED" placement="bottom" mouseEnterDelay={0.6}>
        <div
          role="img"
          aria-label="HDD LED"
          className={clsx(
            'flex h-[30px] w-[24px] cursor-default items-center justify-center transition-colors',
            isHddActive
              ? 'animate-pulse text-amber-400 drop-shadow-[0_0_4px_currentColor]'
              : 'text-neutral-600'
          )}
        >
          <HardDriveIcon size={17} />
        </div>
      </Tooltip>
    </div>
  );
};
