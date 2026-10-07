import { useAuth } from '@/contexts/auth.ts';
import { Button } from 'antd';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import { SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { Logout } from './logout.tsx';
import { Users } from './users.tsx';

export const Account = () => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { account } = useAuth();

  function changePassword() {
    navigate('/auth/password');
  }

  return (
    <div className="space-y-6">
      <SettingsSection>
        <SettingRow label={t('settings.account.webAccount')}>
          <span>{account.username}</span>
        </SettingRow>

        <SettingRow label={t('settings.account.role')}>
          <span>{t(`settings.account.roles.${account.role}`)}</span>
        </SettingRow>

        <SettingRow label={t('settings.account.password')}>
          <Button type="primary" onClick={changePassword}>
            {t('settings.account.updateBtn')}
          </Button>
        </SettingRow>
      </SettingsSection>

      {account.role === 'admin' && <Users />}

      <Logout />
    </div>
  );
};
