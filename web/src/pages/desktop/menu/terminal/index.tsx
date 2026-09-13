import { useCallback, useEffect, useRef, useState } from 'react';
import { Divider } from 'antd';
import { SquareTerminalIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getVirtualDevice, usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { MenuItem } from '@/components/menu-item.tsx';
import { openSerialTerminal } from '@/pages/terminal/launch.ts';

import { Nanokvm } from './nanokvm';
import { SerialPort } from './serial-port';

export const Terminal = () => {
  const { t } = useTranslation();
  const [serialEnabled, setSerialEnabled] = useState(false);
  const requestGeneration = useRef(0);
  const refresh = useCallback(async () => {
    const generation = ++requestGeneration.current;
    try {
      const response = await getVirtualDevice();
      if (generation === requestGeneration.current)
        setSerialEnabled(response.code === 0 && response.data?.serial === true);
    } catch {
      if (generation === requestGeneration.current) setSerialEnabled(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
    const changed = () => {
      void refresh();
    };
    window.addEventListener(usbCompositionChangedEvent, changed);
    return () => {
      ++requestGeneration.current;
      window.removeEventListener(usbCompositionChangedEvent, changed);
    };
  }, [refresh]);

  const content = (
    <div className="min-w-[200px]">
      <div className="flex items-center justify-between px-1">
        <span className="text-base font-bold text-neutral-300">{t('terminal.title')}</span>
      </div>

      <Divider style={{ margin: '10px 0 15px 0' }} />

      <Nanokvm />
      {serialEnabled && (
        <button
          type="button"
          className="flex h-[28px] items-center space-x-1 rounded border-0 bg-transparent px-2 py-1 text-inherit select-none hover:bg-neutral-700/70 hover:text-inherit"
          onClick={() =>
            openSerialTerminal(new URLSearchParams({ port: '/dev/ttyGS0' }).toString())
          }
        >
          <SquareTerminalIcon size={14} />
          <span>{t('terminal.usbSerial')}</span>
        </button>
      )}
      <SerialPort />
    </div>
  );

  return (
    <MenuItem
      title={t('terminal.title')}
      icon={<SquareTerminalIcon size={18} />}
      content={content}
      fresh
      onOpenChange={(open) => {
        if (open) void refresh();
      }}
    />
  );
};
