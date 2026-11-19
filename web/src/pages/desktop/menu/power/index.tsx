import { useEffect, useState } from 'react';
import { Divider, type PopoverProps } from 'antd';
import clsx from 'clsx';
import { useAtomValue } from 'jotai';
import { HardDriveIcon, LoaderCircleIcon, PowerIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { subscribeLEDStatus } from '@/lib/led-status.ts';
import { hostLedStatusVisibleAtom } from '@/jotai/settings.ts';
import { MenuItem } from '@/components/menu-item.tsx';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';

import { PowerLong } from './power-long.tsx';
import { PowerShort } from './power-short.tsx';
import { Reset } from './reset.tsx';

// label, className, placement: the button outside a toolbar, with its title
// beside the icon (see MenuItem); the disk LED is then shown elsewhere.
export const Power = ({
  vertical = false,
  label = false,
  className,
  placement
}: {
  vertical?: boolean;
  label?: boolean;
  className?: string;
  placement?: PopoverProps['placement'];
}) => {
  const { t } = useTranslation();

  const [isPowerOn, setIsPowerOn] = useState(false);
  const [isHddActive, setIsHddActive] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  // The indicator panel beside the menu shows the disk LED already (the
  // panel and this button both need ATX power control).
  const hddInPanel = useAtomValue(hostLedStatusVisibleAtom);

  useEffect(
    () =>
      subscribeLEDStatus((status) => {
        setIsPowerOn(status?.pwr ?? false);
        setIsHddActive(status?.hdd ?? false);
      }),
    []
  );

  const icon = (
    <div
      className={clsx(
        'h-[18px] w-[18px]',
        isPowerOn
          ? 'text-green-500 drop-shadow-[0_0_4px_currentColor]'
          : 'hover:text-fg-strong text-neutral-300'
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
      </div>

      <Divider style={{ margin: '10px 0 15px 0' }} />

      <div className="flex flex-col space-y-1">
        <Reset isLoading={isLoading} setIsLoading={setIsLoading} />
        <PowerShort isLoading={isLoading} setIsLoading={setIsLoading} />
        <PowerLong isLoading={isLoading} setIsLoading={setIsLoading} />
      </div>
    </div>
  );

  if (label)
    return (
      <MenuItem
        title={t('power.title')}
        icon={icon}
        content={content}
        label
        className={className}
        placement={placement}
      />
    );

  return (
    <div className={clsx('flex shrink-0 items-center', vertical && 'flex-col')}>
      <MenuItem title={t('power.title')} icon={icon} content={content} />
      {!hddInPanel && (
        <MenuTooltip title={t('power.hddLed')}>
          <div
            role="img"
            aria-label={t('power.hddLed')}
            className={clsx(
              'flex h-[30px] cursor-default items-center justify-center',
              vertical ? 'w-[30px]' : 'w-[24px]',
              isHddActive ? 'text-red-400 drop-shadow-[0_0_4px_currentColor]' : 'text-neutral-600'
            )}
          >
            <HardDriveIcon size={17} />
          </div>
        </MenuTooltip>
      )}
    </div>
  );
};
