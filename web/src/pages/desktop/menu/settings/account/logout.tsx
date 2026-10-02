import { LogoutOutlined } from '@ant-design/icons';
import { Button, message, Popconfirm } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import * as api from '@/api/auth.ts';
import { notifyAuthExpired } from '@/lib/auth-events.ts';

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
    <div className="flex justify-center pt-3">
      <Popconfirm
        placement="bottom"
        title={t('settings.account.logoutDesc')}
        okText={t('settings.account.okBtn')}
        cancelText={t('settings.account.cancelBtn')}
        onConfirm={logout}
      >
        <Button danger type="primary" size="large" shape="round" icon={<LogoutOutlined />}>
          {t('settings.account.logoutBtn')}
        </Button>
      </Popconfirm>
    </div>
  );
};
