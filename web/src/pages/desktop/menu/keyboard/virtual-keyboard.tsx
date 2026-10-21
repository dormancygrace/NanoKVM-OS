import { useContext } from 'react';
import clsx from 'clsx';
import { useSetAtom } from 'jotai';
import { KeyboardIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { isKeyboardOpenAtom } from '@/jotai/keyboard.ts';
import { MobileMenuItemContext } from '@/components/mobile-menu-context';

export const VirtualKeyboard = () => {
  const { t } = useTranslation();
  const { onRequestMobileMenuDismiss } = useContext(MobileMenuItemContext);
  const setIsKeyboardOpen = useSetAtom(isKeyboardOpenAtom);

  return (
    <div
      className={clsx(
        'flex cursor-pointer items-center space-x-2 rounded py-1 pr-5 pl-2 select-none hover:bg-neutral-700/70'
      )}
      onClick={() => {
        setIsKeyboardOpen((o) => !o);
        onRequestMobileMenuDismiss?.();
      }}
    >
      <KeyboardIcon size={18} />
      <span>{t('keyboard.virtual')}</span>
    </div>
  );
};
