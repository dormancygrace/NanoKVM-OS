import { useState } from 'react';
import { message } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/hid.ts';
import { CtrlAltDel as CtrlAltDelIcon } from '@/components/icons/ctrl-alt-del.tsx';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';
import { confirmAction } from '@/components/ui/confirm.ts';

const inputOwnerRequiredCode = -4;

// Ctrl+Alt+Del in one click: the browser keeps the real keys to itself. The
// NanoKVM presses the keys (see Key sequences in the keyboard menu).
export const CtrlAltDel = () => {
  const { t } = useTranslation();
  const [busy, setBusy] = useState(false);

  async function send() {
    if (busy) return;
    // On a Linux text console the combination restarts the computer.
    const ok = await confirmAction({
      title: t('keyboard.ctrlAltDel.confirmTitle'),
      content: t('keyboard.ctrlAltDel.confirm'),
      okText: t('keyboard.sequences.send'),
      danger: true
    });
    if (!ok) return;
    setBusy(true);
    try {
      const rsp = await api.sendKeySequence('ctrl-alt-del');
      if (rsp.code === inputOwnerRequiredCode) {
        void message.error(t('keyboard.sequences.notController'));
      } else if (rsp.code !== 0) {
        void message.error(t('keyboard.sequences.failed'));
      }
    } catch {
      void message.error(t('keyboard.sequences.failed'));
    }
    setBusy(false);
  }

  return (
    <MenuTooltip title={t('keyboard.ctrlAltDel.send')}>
      <div
        role="button"
        tabIndex={0}
        aria-label={t('keyboard.ctrlAltDel.send')}
        aria-busy={busy}
        data-toolbar-control
        className="hover:text-fg-strong flex h-[30px] w-[30px] cursor-pointer items-center justify-center rounded text-neutral-300 select-none hover:bg-neutral-700/80 aria-busy:opacity-50"
        onClick={() => void send()}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault();
            void send();
          }
        }}
      >
        <CtrlAltDelIcon size={18} />
      </div>
    </MenuTooltip>
  );
};
