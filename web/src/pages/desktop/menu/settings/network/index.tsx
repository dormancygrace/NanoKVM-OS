import { SettingsSection } from '@/components/ui/settings.tsx';

import { DNS } from './dns.tsx';
import { Ethernet } from './ethernet.tsx';
import { Gateway } from './gateway.tsx';
import { NetworkInformation } from './information.tsx';
import { IPv6 } from './ipv6.tsx';
import { Mdns } from './mdns';
import { Tls } from './tls.tsx';
import { Wifi } from './wifi.tsx';

export const Network = () => (
  <div className="space-y-6">
    <SettingsSection>
      <NetworkInformation />
    </SettingsSection>
    <Gateway />
    <DNS />
    <SettingsSection>
      <IPv6 />
      <Mdns />
      <Tls />
    </SettingsSection>
  </div>
);

export const WifiSettings = () => <Wifi />;

export const EthernetSettings = () => <Ethernet />;
