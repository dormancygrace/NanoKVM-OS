import { useEffect, useMemo } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button, notification } from 'antd';
import { useAtom, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { availableUpdates, getUpdateNotice, type UpdateNotice } from '@/api/updates.ts';
import {
  componentLabel,
  getSeenUpdates,
  setSeenUpdates,
  updateSignature
} from '@/lib/update-components.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { settingsRequestAtom } from '@/jotai/settings.ts';
import { updateNoticeAtom } from '@/jotai/updates.ts';

const NOTIFICATION_KEY = 'nanokvm-os-updates';
// The server checks once a day; reading its result is a file read.
const POLL_MS = 30 * 60 * 1000;

// Tells administrators once per new set of versions that NanoKVM OS updates
// are available. The settings badge keeps showing until they are installed.
export const UpdateNotification = () => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const isAdmin = account.role === 'admin';
  const [notice, setNotice] = useAtom(updateNoticeAtom);
  const setRequest = useSetAtom(settingsRequestAtom);
  const [api, contextHolder] = notification.useNotification();

  useEffect(() => {
    if (!isAdmin) return;
    let active = true;
    const refresh = async () => {
      try {
        const rsp = await getUpdateNotice();
        if (active && rsp.code === 0) setNotice(rsp.data as UpdateNotice);
      } catch {
        /* Keep the last known state through brief disconnects. */
      }
    };
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), POLL_MS);
    return () => {
      active = false;
      stop();
    };
  }, [isAdmin, setNotice]);

  const updates = useMemo(() => (isAdmin ? availableUpdates(notice) : []), [isAdmin, notice]);
  const signature = updateSignature(updates);
  useEffect(() => {
    if (!signature) {
      api.destroy(NOTIFICATION_KEY);
      return;
    }
    if (getSeenUpdates() === signature) return;
    setSeenUpdates(signature);
    api.info({
      key: NOTIFICATION_KEY,
      title: t('settings.updates.notice.title'),
      description: (
        <ul className="m-0 list-none p-0 text-sm">
          {updates.map((update) => (
            <li key={update.name}>
              {componentLabel(t, update.name)}: {update.installed} → {update.available}
            </li>
          ))}
        </ul>
      ),
      actions: (
        <Button
          type="primary"
          size="small"
          onClick={() => {
            api.destroy(NOTIFICATION_KEY);
            setRequest('system-updates');
          }}
        >
          {t('settings.updates.notice.open')}
        </Button>
      ),
      placement: 'topRight',
      duration: 0
    });
  }, [signature, updates, api, t, setRequest]);

  return <>{contextHolder}</>;
};
