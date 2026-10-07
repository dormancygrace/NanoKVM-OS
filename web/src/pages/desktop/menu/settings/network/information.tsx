import { useEffect, useState } from 'react';
import { EthernetPortIcon, WifiIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getInfo } from '@/api/vm';
import { SettingRow } from '@/components/ui/settings.tsx';

import { Hostname } from './hostname';

type NetworkInfo = { ips: { addr: string; type: string }[]; mdns: string };

export const NetworkInformation = () => {
  const { t } = useTranslation();
  const [info, setInfo] = useState<NetworkInfo>();
  useEffect(() => {
    let active = true;
    getInfo()
      .then((rsp) => {
        if (active && rsp.code === 0) setInfo(rsp.data);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);
  return (
    <>
      <Hostname editable />
      <SettingRow label={t('settings.about.ip')}>
        <div className="min-w-0 space-y-1 text-right">
          {info?.ips?.length
            ? info.ips.map((ip) => (
                <div key={ip.addr} className="flex items-center justify-end gap-2">
                  <span className="break-all">{ip.addr}</span>
                  {ip.type === 'Wireless' ? (
                    <WifiIcon size={16} className="text-fg-muted shrink-0" />
                  ) : (
                    <EthernetPortIcon size={16} className="text-fg-muted shrink-0" />
                  )}
                </div>
              ))
            : '—'}
        </div>
      </SettingRow>
      {info?.mdns && (
        <SettingRow label="mDNS">
          <span className="text-right break-all">{info.mdns}</span>
        </SettingRow>
      )}
    </>
  );
};
