import { useSyncExternalStore } from 'react';
import { Button, Tooltip } from 'antd';
import { LockKeyholeIcon, UnlockKeyholeIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { client } from '@/lib/websocket.ts';

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
        onClick={() => client.requestControl(!enabled)}
        icon={enabled ? <UnlockKeyholeIcon size={18} /> : <LockKeyholeIcon size={18} />}
      />
    </Tooltip>
  );
};
