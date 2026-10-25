import { useContext } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { useAtom } from 'jotai';
import { EllipsisIcon, KeyboardIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { releasePointerMenuFocus } from '@/lib/menu-focus.ts';
import { isKeyboardOpenAtom } from '@/jotai/keyboard.ts';
import { MenuItem } from '@/components/menu-item.tsx';
import { MobileMenuItemContext } from '@/components/mobile-menu-context.ts';

import { LeaderKey } from './leader-key.tsx';
import { Paste } from './paste.tsx';
import { Sequences } from './sequences.tsx';
import { Shortcuts } from './shortcuts';
import { VirtualKeyboard } from './virtual-keyboard.tsx';

export const Keyboard = ({ quickAccess = false }: { quickAccess?: boolean }) => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const [isKeyboardOpen, setKeyboardOpen] = useAtom(isKeyboardOpenAtom);
  const { onRequestMobileMenuDismiss } = useContext(MobileMenuItemContext);

  const content = (
    <div className="flex flex-col space-y-1">
      <Paste />
      <VirtualKeyboard />
      <Shortcuts />
      <Sequences />
      {account.role === 'admin' && <LeaderKey />}
    </div>
  );

  return (
    <>
      {quickAccess && (
        <button
          type="button"
          aria-label={t('keyboard.virtual')}
          aria-pressed={isKeyboardOpen}
          className="nanokvm-button-base flex size-10 shrink-0 items-center justify-center rounded text-neutral-300 hover:bg-neutral-700/80 hover:text-white aria-pressed:bg-neutral-700/80 aria-pressed:text-emerald-400"
          onClick={(event) => {
            releasePointerMenuFocus(event);
            setKeyboardOpen((open) => !open);
            onRequestMobileMenuDismiss?.();
          }}
        >
          <KeyboardIcon size={18} />
        </button>
      )}
      <MenuItem
        title={t('keyboard.title')}
        icon={quickAccess ? <EllipsisIcon size={18} /> : <KeyboardIcon size={18} />}
        content={content}
      />
    </>
  );
};
