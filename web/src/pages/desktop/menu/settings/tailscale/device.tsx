import { useEffect, useState } from 'react';
import { Button, Popconfirm, Switch } from 'antd';
import { LogOutIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/tailscale.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { Status } from './types.ts';

type DeviceProps = {
  status: Status;
  onLogout: () => void;
};

export const Device = ({ status, onLogout }: DeviceProps) => {
  const { t } = useTranslation();

  const [isRunning, setIsRunning] = useState(false);
  const [isUpdating, setIsUpdating] = useState(false);
  const [isLogging, setIsLogging] = useState(false);

  useEffect(() => {
    setIsRunning(status.state === 'running');
  }, [status]);

  async function update() {
    if (isUpdating) return;
    setIsUpdating(true);

    try {
      const rsp = isRunning ? await api.down() : await api.up();
      if (rsp.code !== 0) {
        showRequestError(rsp);
        return;
      }

      setIsRunning(!isRunning);
    } finally {
      setIsUpdating(false);
    }
  }

  async function logout() {
    if (isLogging) return;
    setIsLogging(true);

    api
      .logout()
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp, 'settings.tailscale.logoutFailed');
          return;
        }

        onLogout();
      })
      .catch((err) => showRequestError(err, 'settings.tailscale.logoutFailed'))
      .finally(() => {
        setIsLogging(false);
      });
  }

  return (
    <div className="space-y-6">
      <SettingsSection>
        <SettingRow label={t('settings.tailscale.enable')} htmlFor="tailscale-enable">
          <Switch id="tailscale-enable" checked={isRunning} loading={isUpdating} onClick={update} />
        </SettingRow>

        <SettingRow label={t('settings.tailscale.deviceName')}>
          <span>{status.name}</span>
        </SettingRow>

        <SettingRow label={t('settings.tailscale.deviceIP')}>
          <span>{status.ip}</span>
        </SettingRow>

        <SettingRow label={t('settings.tailscale.account')}>
          <span>{status.account}</span>
        </SettingRow>
      </SettingsSection>

      <div className="flex justify-center">
        <Popconfirm
          placement="bottom"
          title={t('settings.tailscale.logoutDesc')}
          okText={t('settings.tailscale.okBtn')}
          cancelText={t('settings.tailscale.cancelBtn')}
          onConfirm={logout}
        >
          <Button
            danger
            type="primary"
            size="large"
            shape="round"
            icon={<LogOutIcon size={16} />}
            loading={isLogging}
          >
            {t('settings.tailscale.logout')}
          </Button>
        </Popconfirm>
      </div>
    </div>
  );
};
