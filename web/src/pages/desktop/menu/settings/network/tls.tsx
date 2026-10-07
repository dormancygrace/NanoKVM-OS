import { useEffect, useState } from 'react';
import axios from 'axios';
import { message, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { HelpTip } from '@/components/ui/settings.tsx';

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
    <div className="flex items-center justify-between">
      <div className="flex flex-col space-y-1">
        <div className="flex items-center space-x-2">
          <span>HTTPS</span>
          <HelpTip title={t('settings.network.tls.tip')} />
        </div>
        <span className="text-xs text-fg-muted">{t('settings.network.tls.description')}</span>
      </div>

      <Switch aria-label="HTTPS" checked={isEnabled} loading={isLoading} onChange={update} />
    </div>
  );
};
