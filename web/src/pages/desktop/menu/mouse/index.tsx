import { useEffect } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Divider } from 'antd';
import { useAtomValue, useSetAtom } from 'jotai';
import { MouseIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  getPointerCapabilities,
  normalizeInputAdapterMode,
  resolveInputAdapter
} from '@/lib/input-adapter.ts';
import * as ls from '@/lib/localstorage';
import {
  effectiveMouseModeAtom,
  inputAdapterAtom,
  mouseModeAtom,
  mouseStyleAtom,
  scrollDirectionAtom,
  scrollIntervalAtom
} from '@/jotai/mouse';
import { usbInputAtom } from '@/jotai/usb-input.ts';
import { MenuItem } from '@/components/menu-item.tsx';

import { OriginalResolution } from '../screen/original-resolution.tsx';
import { Cursor } from './cursor.tsx';
import { Direction } from './direction.tsx';
import { InputAdapter } from './input-adapter.tsx';
import { MouseMode } from './mouse-mode.tsx';
import { ResetHid } from './reset-hid.tsx';
import { Speed } from './speed.tsx';
import { TouchpadGuide } from './touchpad-guide.tsx';

export const Mouse = ({ hidden = false }: { hidden?: boolean }) => {
  const { t } = useTranslation();
  const usbInput = useAtomValue(usbInputAtom);
  const mode = useAtomValue(effectiveMouseModeAtom);
  const adapter = useAtomValue(inputAdapterAtom);
  const capabilities = getPointerCapabilities();
  const touchpad = mode && resolveInputAdapter(adapter, mode, capabilities) === 'touchpad';
  const isAdmin = useAuth().account.role === 'admin';

  const setMouseStyle = useSetAtom(mouseStyleAtom);
  const setMouseMode = useSetAtom(mouseModeAtom);
  const setInputAdapter = useSetAtom(inputAdapterAtom);
  const setScrollDirection = useSetAtom(scrollDirectionAtom);
  const setScrollInterval = useSetAtom(scrollIntervalAtom);

  useEffect(() => {
    const mouseStyle = ls.getMouseStyle();
    if (mouseStyle) {
      setMouseStyle(mouseStyle);
    }

    const mouseMode = ls.getMouseMode();
    if (mouseMode) {
      setMouseMode(mouseMode);
    }

    setInputAdapter(normalizeInputAdapterMode(ls.getInputAdapter()));

    const direction = ls.getMouseScrollDirection();
    if (direction) {
      setScrollDirection(direction > 0 ? 1 : -1);
    }

    const interval = ls.getMouseScrollInterval();
    if (interval) {
      setScrollInterval(interval);
    }
  }, [setMouseStyle, setMouseMode, setInputAdapter, setScrollDirection, setScrollInterval]);

  // Initialize saved pointer preferences even when the toolbar button is hidden.
  if (hidden) return null;

  const content = (
    <div className="flex flex-col space-y-1">
      {capabilities.finePointer && <Cursor />}
      {usbInput.absolute && usbInput.relative && <MouseMode />}
      {mode === 'relative' && capabilities.canPointerLock && capabilities.finePointer && (
        <InputAdapter />
      )}
      <Direction />
      <Speed />
      {((isAdmin &&
        (usbInput.relative || (mode === 'absolute' && usbInput.pointerProfile !== 'windows'))) ||
        (mode === 'relative' && touchpad)) && <Divider style={{ margin: '10px 0' }} />}

      {isAdmin && mode === 'absolute' && usbInput.pointerProfile !== 'windows' && (
        <OriginalResolution />
      )}
      {isAdmin && usbInput.relative && <ResetHid />}
      {mode === 'relative' && touchpad && <TouchpadGuide />}
    </div>
  );

  return <MenuItem title={t('mouse.title')} icon={<MouseIcon size={18} />} content={content} />;
};
