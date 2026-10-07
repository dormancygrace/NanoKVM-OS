import { useEffect, useState } from 'react';

import { http } from '@/lib/http.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';

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
    const stopPolling = pollWhileVisible(refresh, 60000);
    return () => {
      active = false;
      stopPolling();
    };
  }, [name]);
  return version ? <div className="text-fg-muted text-xs">v{version}</div> : null;
}
