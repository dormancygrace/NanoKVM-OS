import { useState } from 'react';
import { Button, Popconfirm, Switch } from 'antd';
import { UnplugIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/netbird.ts';
import { SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { ErrorHelp } from './error-help.tsx';
import { LoginUrl } from './login-url.tsx';
import { Status } from './types.ts';

type DeviceProps = {
  status: Status;
  onLogout: () => void;
};

export const Device = ({ status, onLogout }: DeviceProps) => {
  const { t } = useTranslation();

  const [isUpdating, setIsUpdating] = useState(false);
  const [isDisconnecting, setIsDisconnecting] = useState(false);
  const [errMsg, setErrMsg] = useState('');
  const [loginUrl, setLoginUrl] = useState('');
  const isBusy = isUpdating || isDisconnecting;

  async function update() {
    if (isBusy) return;
    setIsUpdating(true);

    try {
      const isRunning = status.state === 'running';
      const rsp = isRunning ? await api.down() : await api.login();
      if (rsp.code !== 0) {
        setErrMsg(rsp.msg);
        return;
      }

      // Enabling an unbound device returns an interactive login URL. Showing it
      // is the whole point: flipping the switch instead would claim the tunnel
      // is up while the device waits for an authorization nobody opened.
      const url = !isRunning ? rsp.data?.url : '';
      if (url) {
        setLoginUrl(url);
        window.open(url, '_blank');
        return;
      }

      // A successful command does not prove that the tunnel has reached the
      // requested state. Keep rendering the last confirmed parent status and
      // obtain a new observation instead of applying an optimistic toggle.
      onLogout();
    } catch (err: any) {
      setErrMsg(err?.message || t('settings.netbird.error.requestFailed'));
    } finally {
      setIsUpdating(false);
    }
  }

  async function disconnect() {
    if (isBusy) return;
    setIsDisconnecting(true);

    api
      .down()
      .then((rsp) => {
        if (rsp.code !== 0) {
          setErrMsg(rsp.msg);
          return;
        }

        onLogout();
      })
      .catch((err) => {
        setErrMsg(err.message || t('settings.netbird.error.disconnectFailed'));
      })
      .finally(() => {
        setIsDisconnecting(false);
      });
  }

  if (loginUrl) {
    return (
      <div className="flex flex-col items-center justify-center space-y-6">
        <LoginUrl url={loginUrl} onConfirm={onLogout} onCancel={() => setLoginUrl('')} />
        {errMsg && <ErrorHelp error={errMsg} onRefresh={onLogout} canRestart />}
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <SettingsSection>
        <SettingRow label={t('settings.netbird.enable')} htmlFor="netbird-enable">
          <Switch
            id="netbird-enable"
            checked={status.state === 'running'}
            disabled={isDisconnecting}
            loading={isUpdating}
            onClick={update}
          />
        </SettingRow>

        <SettingRow label={t('settings.netbird.deviceName')}>
          <span>{status.name}</span>
        </SettingRow>

        <SettingRow label={t('settings.netbird.deviceIP')}>
          <span>{status.ip}</span>
        </SettingRow>

        <SettingRow label={t('settings.netbird.version')}>
          <span>{status.version}</span>
        </SettingRow>
      </SettingsSection>

      <div className="flex justify-center">
        <Popconfirm
          placement="bottom"
          title={t('settings.netbird.disconnectConfirm')}
          okText={t('settings.netbird.okBtn')}
          cancelText={t('settings.netbird.cancelBtn')}
          onConfirm={disconnect}
        >
          <Button
            danger
            type="primary"
            size="large"
            shape="round"
            icon={<UnplugIcon size={16} />}
            disabled={isUpdating}
            loading={isDisconnecting}
          >
            {t('settings.netbird.disconnect')}
          </Button>
        </Popconfirm>
      </div>

      {errMsg && <ErrorHelp error={errMsg} onRefresh={onLogout} canRestart />}
    </div>
  );
};
