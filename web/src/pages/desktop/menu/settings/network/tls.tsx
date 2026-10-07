import { useEffect, useState } from 'react';
import { message, Switch } from 'antd';
import axios from 'axios';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { confirmAction } from '@/components/ui/confirm.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

function restartInterruptedRequest(err: unknown): boolean {
  if (!axios.isAxiosError(err)) return false;
  const status = err.response?.status;
  return status === undefined || (status >= 502 && status <= 504);
}

export const Tls = () => {
  const { t } = useTranslation();

  const [isEnabled, setIsEnabled] = useState(false);
  const [isLoading, setIsLoading] = useState(false);

  useEffect(() => {
    setIsEnabled(window.location.protocol === 'https:');
  }, []);

  async function update() {
    if (isLoading) return;
    setIsLoading(true);

    const enable = !isEnabled;
    const confirmed = await confirmAction({
      title: t(
        enable ? 'settings.network.tls.confirmEnable' : 'settings.network.tls.confirmDisable'
      ),
      content: t('settings.network.tls.confirmRestart'),
      danger: true
    });
    if (!confirmed) {
      // The switch shows loading while the dialog is open; clear it on cancel.
      setIsLoading(false);
      return;
    }

    try {
      const rsp = await api.setTLS(enable);
      if (rsp.code !== 0) {
        message.error(rsp.msg || t('settings.network.tls.failed'));
        setIsLoading(false);
        return;
      }
    } catch (err) {
      // The server restarts itself to apply the change and may drop the
      // connection before its reply arrives (or a proxy may answer 502-504
      // meanwhile). Only a definite HTTP refusal means it was not applied.
      if (!restartInterruptedRequest(err)) {
        console.log(err);
        message.error(t('settings.network.tls.failed'));
        setIsLoading(false);
        return;
      }
    }
    setIsEnabled(enable);

    // The server restarts after accepting the change; reload once it is back.
    const seconds = enable ? 30 : 10;
    setTimeout(() => {
      reload(enable);
    }, seconds * 1000);
  }

  function reload(enable: boolean) {
    if (!enable) {
      // Plain HTTP listens on its own port; do not reuse the HTTPS one.
      const target = new URL(window.location.href);
      target.protocol = 'http:';
      target.port = '';
      window.open(target.toString(), '_blank');
    }

    window.location.reload();
  }

  return (
    <SettingRow
      label="HTTPS"
      description={t('settings.network.tls.description')}
      help={t('settings.network.tls.tip')}
      htmlFor="network-tls"
    >
      <Switch
        id="network-tls"
        aria-label="HTTPS"
        aria-describedby="network-tls-description"
        checked={isEnabled}
        loading={isLoading}
        onChange={update}
      />
    </SettingRow>
  );
};
