import { useSyncExternalStore } from 'react';
import { Button, Tooltip } from 'antd';
import { useAtomValue } from 'jotai';
import { LockKeyholeIcon, UnlockKeyholeIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { client } from '@/lib/websocket.ts';
import { usbInputAtom } from '@/jotai/usb-input.ts';

export const Control = () => {
  const { t } = useTranslation();
  const enabled = useSyncExternalStore(
    client.subscribeControlStatus,
    client.getControlEnabled,
    client.getControlEnabled
  );

  const label = enabled ? t('sessionControl.release') : t('sessionControl.take');
  return (
    <Tooltip title={enabled ? t('sessionControl.active') : t('sessionControl.locked')}>
      <Button
        type="text"
        aria-label={label}
        className={enabled ? undefined : '!bg-amber-500/20 !text-amber-300'}
        onClick={() => client.requestControl(!enabled)}
        icon={enabled ? <UnlockKeyholeIcon size={18} /> : <LockKeyholeIcon size={18} />}
      />
    </Tooltip>
  );
};

// A tooltip alone is invisible to touch users.
export const ControlNotice = () => {
  const input = useAtomValue(usbInputAtom);
  const enabled = useSyncExternalStore(client.subscribeControlStatus, client.getControlEnabled);
  const connection = useSyncExternalStore(
    client.subscribeConnectionStatus,
    client.getConnectionStatus
  );
  const { t } = useTranslation();
  if (!input.available || enabled || connection !== 'connected') return null;
  return (
    <div className="fixed top-3 left-1/2 z-900 -translate-x-1/2" role="status">
      <Button
        className="!border-amber-500/50 !bg-neutral-900/95 !text-amber-200 shadow-lg"
        icon={<LockKeyholeIcon size={16} />}
        onClick={() => client.requestControl(true)}
      >
        {t('sessionControl.viewOnly')} · {t('sessionControl.take')}
      </Button>
    </div>
  );
};
