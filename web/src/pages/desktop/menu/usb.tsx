import { useEffect, useState } from 'react';
import { useSetAtom } from 'jotai';
import { UsbIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { MenuItem } from '@/components/menu-item.tsx';

import { Usb } from './settings/usb';

export const UsbMenu = () => {
  const { t } = useTranslation();
  const lock = useSetAtom(keyboardLockAtom);
  const [open, setOpen] = useState(false);
  useEffect(() => () => lock({ source: 'usb-popover', locked: false }), [lock]);
  return (
    <MenuItem
      title={t('settings.usb.title')}
      icon={<UsbIcon size={18} />}
      fresh
      onOpenChange={(value) => {
        setOpen(value);
        lock({ source: 'usb-popover', locked: value });
      }}
      content={
        <div className="max-h-[75vh] w-[min(440px,85vw)] overflow-y-auto p-1">
          {open && <Usb />}
        </div>
      }
    />
  );
};
