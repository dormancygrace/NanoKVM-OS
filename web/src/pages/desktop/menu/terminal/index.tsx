import { useCallback, useEffect, useRef, useState } from 'react';
import { Divider } from 'antd';
import { SquareTerminalIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getVirtualDevice, usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { MenuItem } from '@/components/menu-item.tsx';

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
        <a
          className="flex h-[28px] select-none items-center space-x-1 rounded px-2 py-1 text-inherit hover:bg-neutral-700/70 hover:text-inherit"
          href="/#terminal?port=%2Fdev%2FttyGS0&baud=115200&parity=none&flowControl=none&dataBits=8&stopBits=1"
          target="_blank"
          rel="noopener noreferrer"
        >
          <SquareTerminalIcon size={14} />
          <span>{t('terminal.usbSerial')}</span>
        </a>
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
