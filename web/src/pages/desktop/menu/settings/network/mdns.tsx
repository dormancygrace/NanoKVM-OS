import { useEffect, useState } from 'react';
import { Switch, Tooltip } from 'antd';
import { CircleAlertIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

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
      if (rsp.code !== 0) return;
      setIsEnabled(next);
      setAddress('');
      if (next) {
        const info = await api.getInfo();
        if (info.code === 0) setAddress(info.data?.mdns || '');
      }
    } catch {
      // Keep the last confirmed state if the request fails.
    } finally {
      setIsLoading(false);
    }
  }

  return (
    <div className="flex items-center justify-between">
      <div className="flex flex-col space-y-1">
        <div className="flex items-center space-x-2">
          <span>mDNS</span>

          <Tooltip
            title={t('settings.device.mdns.tip')}
            className="cursor-pointer"
            placement="right"
            styles={{ root: { maxWidth: '400px' } }}
          >
            <CircleAlertIcon className="text-neutral-500" size={14} />
          </Tooltip>
        </div>

        <span className="text-xs text-neutral-500">
          {isEnabled && address ? address : t('settings.device.mdns.description')}
        </span>
      </div>

      <Switch aria-label="mDNS" checked={isEnabled} loading={isLoading} onChange={update} />
    </div>
  );
};
