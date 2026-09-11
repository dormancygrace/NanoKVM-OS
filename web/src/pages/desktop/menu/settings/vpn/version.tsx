import { useEffect, useState } from 'react';

import { http } from '@/lib/http.ts';

export function VPNVersion({ name }: { name: 'tailscale' | 'wireguard' | 'openvpn' }) {
  const [version, setVersion] = useState('');
  useEffect(() => {
    let active = true;
    const refresh = () =>
      void http
        .get('/api/extensions/vpn/versions')
        .then((rsp) => {
          if (active && rsp.code === 0) setVersion(rsp.data[name] || '');
        })
        .catch(() => {});
    refresh();
    const timer = window.setInterval(refresh, 60000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [name]);
  return version ? (
    <span className="ml-2 text-xs font-normal text-neutral-500">v{version}</span>
  ) : null;
}
