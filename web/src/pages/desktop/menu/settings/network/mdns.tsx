import { useEffect, useState } from 'react';
import { Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { HelpTip } from '@/components/ui/settings.tsx';

export const Mdns = () => {
  const { t } = useTranslation();

  const [isEnabled, setIsEnabled] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [address, setAddress] = useState('');

  useEffect(() => {
    let active = true;
    Promise.allSettled([api.getMdnsState(), api.getInfo()]).then(([state, info]) => {
      if (!active) return;
      if (state.status === 'fulfilled' && state.value.code === 0) {
        setIsEnabled(!!state.value.data?.enabled);
      }
      if (info.status === 'fulfilled' && info.value.code === 0) {
        setAddress(info.value.data?.mdns || '');
      }
      setIsLoading(false);
    });
    return () => {
      active = false;
    };
  }, []);

  async function update() {
    if (isLoading) return;
    setIsLoading(true);
    try {
      const next = !isEnabled;
      const rsp = next ? await api.enableMdns() : await api.disableMdns();
      if (rsp.code !== 0) {
        showRequestError(rsp);
        return;
      }
      setIsEnabled(next);
      setAddress('');
      if (next) {
        const info = await api.getInfo();
        if (info.code === 0) setAddress(info.data?.mdns || '');
      }
    } catch (err) {
      // Keep the last confirmed state if the request fails.
      showRequestError(err);
    } finally {
      setIsLoading(false);
    }
  }

  return (
    <div className="flex items-center justify-between">
      <div className="flex flex-col space-y-1">
        <div className="flex items-center space-x-2">
          <span>mDNS</span>
          <HelpTip title={t('settings.device.mdns.tip')} />
        </div>

        <span className="text-xs text-neutral-500">
          {isEnabled && address ? address : t('settings.device.mdns.description')}
        </span>
      </div>

      <Switch aria-label="mDNS" checked={isEnabled} loading={isLoading} onChange={update} />
    </div>
  );
};
