import { useEffect, useState } from 'react';
import { EthernetPortIcon, WifiIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getInfo } from '@/api/vm';

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
    <div className="space-y-5">
      <Hostname editable />
      <div className="flex items-start justify-between gap-4">
        <span>{t('settings.about.ip')}</span>
        <div className="min-w-0 space-y-1 text-right">
          {info?.ips?.length
            ? info.ips.map((ip) => (
                <div key={ip.addr} className="flex items-center justify-end gap-2">
                  <span className="break-all">{ip.addr}</span>
                  {ip.type === 'Wireless' ? (
                    <WifiIcon size={16} className="shrink-0 text-neutral-500" />
                  ) : (
                    <EthernetPortIcon size={16} className="shrink-0 text-neutral-500" />
                  )}
                </div>
              ))
            : '—'}
        </div>
      </div>
      {info?.mdns && (
        <div className="flex items-start justify-between gap-4">
          <span>mDNS</span>
          <span className="break-all text-right">{info.mdns}</span>
        </div>
      )}
    </div>
  );
};
