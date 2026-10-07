import { Button, message, Popconfirm } from 'antd';
import { LogOutIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import * as api from '@/api/auth.ts';
import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const Logout = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();

  function logout() {
    api
      .logout()
      .then((rsp) => {
        // If the server could not revoke the session it stays valid; say so
        // instead of pretending the user has logged out.
        if (rsp.code !== 0) {
          message.error(rsp.msg || t('settings.account.logoutFailed'));
          return;
        }

        notifyAuthExpired();
        navigate('/auth/login');
      })
      .catch(() => message.error(t('settings.account.logoutFailed')));
  }

  return (
    <SettingRow label={t('settings.account.logoutBtn')}>
      <Popconfirm
        placement="bottomRight"
        title={t('settings.account.logoutDesc')}
        okText={t('settings.account.okBtn')}
        cancelText={t('settings.account.cancelBtn')}
        onConfirm={logout}
      >
        <Button danger icon={<LogOutIcon size={16} />}>
          {t('settings.account.logoutBtn')}
        </Button>
      </Popconfirm>
    </SettingRow>
  );
};
