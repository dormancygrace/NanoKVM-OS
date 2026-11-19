import { useCallback, useEffect, useRef, useState } from 'react';
import { Divider, type PopoverProps } from 'antd';
import { SquareTerminalIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getTerminalSessions, type TerminalSession } from '@/api/terminal.ts';
import { getVirtualDevice, usbCompositionChangedEvent } from '@/api/virtual-device.ts';
import { MenuItem } from '@/components/menu-item.tsx';
import { openSerialTerminal } from '@/pages/terminal/launch.ts';

import { Nanokvm } from './nanokvm';
import { SerialPort } from './serial-port';
import { Sessions } from './sessions';

// label, className, placement: the button outside a toolbar (see MenuItem).
export const Terminal = ({
  label = false,
  className,
  placement
}: {
  label?: boolean;
  className?: string;
  placement?: PopoverProps['placement'];
} = {}) => {
  const { t } = useTranslation();
  const [serialEnabled, setSerialEnabled] = useState(false);
  const [sessions, setSessions] = useState<TerminalSession[]>([]);
  const [maxSessions, setMaxSessions] = useState(0);
  const requestGeneration = useRef(0);
  const refresh = useCallback(async () => {
    const generation = ++requestGeneration.current;
    const [device, shells] = await Promise.allSettled([getVirtualDevice(), getTerminalSessions()]);
    if (generation !== requestGeneration.current) return;
    setSerialEnabled(
      device.status === 'fulfilled' && device.value.code === 0 && device.value.data?.serial === true
    );
    const list =
      shells.status === 'fulfilled' && shells.value.code === 0 ? shells.value.data : null;
    setSessions(list?.sessions ?? []);
    setMaxSessions(list?.max ?? 0);
  }, []);

  useEffect(() => {
    void refresh();
    const changed = () => {
      void refresh();
    };
    window.addEventListener(usbCompositionChangedEvent, changed);
    return () => {
      requestGeneration.current += 1;
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
      <Sessions sessions={sessions} max={maxSessions} onChange={() => void refresh()} />
    </div>
  );

  return (
    <MenuItem
      title={t('terminal.title')}
      icon={<SquareTerminalIcon size={18} />}
      content={content}
      fresh
      label={label}
      className={className}
      placement={placement}
      onOpenChange={(open) => {
        if (open) void refresh();
      }}
    />
  );
};
