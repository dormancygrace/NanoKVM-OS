import { Divider } from 'antd';

import { DNS } from './dns.tsx';
import { Ethernet } from './ethernet.tsx';
import { Gateway } from './gateway.tsx';
import { IPv6 } from './ipv6.tsx';
import { Mdns } from './mdns';
import { Wifi } from './wifi.tsx';
import { NetworkInformation } from './information.tsx';
import { Tls } from './tls.tsx';

export const Network = () => (
  <div className="flex flex-col space-y-6">
    <NetworkInformation />
    <Gateway />
    <DNS />
    <IPv6 />
    <Mdns />
    <Tls />
  </div>
);

export const WifiSettings = () => <Wifi />;

export const EthernetSettings = () => (
  <>
    <div className="text-base">Ethernet</div>
    <Divider className="opacity-50" />
    <Ethernet />
  </>
);
