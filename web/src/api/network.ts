import { http } from '@/lib/http.ts';

export type DNSMode = 'manual' | 'dhcp';
export type EthernetMode = 'dhcp' | 'static';

export type EthernetConfig = {
  enabled: boolean;
  adminUp: boolean;
  linkUp: boolean;
  mode: EthernetMode;
  interface: string;
  address: string;
  subnetMask: string;
  gateway: string;
  vlanEnabled: boolean;
  vlanId: number;
};

// wake on lan
export function wol(mac: string, networkInterface: string) {
  const data = {
    mac,
    interface: networkInterface
  };
  return http.post('/api/network/wol', data);
}

export function getWolInterfaces() {
  return http.get('/api/network/wol/interfaces');
}

// get wake-on-lan macs history
export function getWolMacs() {
  return http.get('/api/network/wol/mac');
}

export function deleteWolMac(mac: string) {
  const data = {
    mac
  };
  return http.delete('/api/network/wol/mac', data);
}

// set Mac name
export function setWolMacName(mac: string, name: string) {
  return http.post('/api/network/wol/mac/name', { mac, name });
}

// get wifi information
export function getWiFi() {
  return http.get('/api/network/wifi');
}

// connect wifi without auth (only available in wifi configuration mode)
export function connectWifiNoAuth(ssid: string, password: string, apPassword?: string) {
  const data = {
    ssid,
    password
  };
  return http.post('/api/network/wifi', data, {
    headers: {
      'X-AP-Key': apPassword || ''
    }
  });
}

// verify ap login
export function verifyApLogin(apPassword: string) {
  return http.post(
    '/api/network/wifi/verify',
    {},
    {
      headers: {
        'X-AP-Key': apPassword || ''
      }
    }
  );
}

// connect wifi
export function connectWifi(ssid: string, password: string) {
  const data = {
    ssid,
    password
  };
  return http.post('/api/network/wifi/connect', data);
}

// disconnect wifi
export function disconnectWifi() {
  return http.post('/api/network/wifi/disconnect');
}

export function getDNS() {
  return http.get('/api/network/dns');
}

export function setDNS(mode: DNSMode, servers: string[]) {
  return http.post('/api/network/dns', { mode, servers });
}

export function getEthernet() {
  return http.get('/api/network/ethernet');
}

export function setEthernet(config: Omit<EthernetConfig, 'interface' | 'adminUp' | 'linkUp'>) {
  return http.post('/api/network/ethernet', config);
}

export type GatewayPreference = 'auto' | 'ethernet' | 'wifi';
export type GatewayRoute = {
  interface: string;
  gateway: string;
  metric: number;
};
export type GatewayStatus = {
  preferred: GatewayPreference;
  routes: GatewayRoute[];
};

export function getGatewayPreference() {
  return http.get('/api/network/gateway');
}

export function setGatewayPreference(preferred: GatewayPreference) {
  return http.post('/api/network/gateway', { preferred });
}

export type IPv6Status = {
  enabled: boolean;
  active: boolean;
  supported: boolean;
  addresses: {
    interface: string;
    address: string;
    scope: 'global' | 'linkLocal' | 'private';
    mtu: number;
  }[];
};

export function getIPv6() {
  return http.get('/api/network/ipv6');
}

export function setIPv6(enabled: boolean) {
  return http.post('/api/network/ipv6', { enabled });
}

export type WifiBand = '2.4' | '5';
export type WifiStatus = {
  supported: boolean;
  enabled: boolean;
  apMode: boolean;
  connected: boolean;
  ssid: string;
  model: string;
  bands: WifiBand[];
  band: WifiBand | '';
  preferredBand: WifiBand;
  busy: boolean;
  error: string;
};
export type WifiSecurity = 'wpa' | 'wpa-wpa2' | 'wpa2' | 'wpa3' | 'wpa2-wpa3' | 'open';
export type WifiNetwork = {
  bssid: string;
  ssid: string;
  band: WifiBand;
  signal: number;
  security: WifiSecurity | 'unsupported';
};
export type WifiProfile = {
  ssid: string;
  password: string;
  band: WifiBand;
  preferredBand?: WifiBand;
  hidden: boolean;
  security: WifiSecurity;
};
export function setWifiEnabled(enabled: boolean) {
  return http.post('/api/network/wifi/enabled', { enabled });
}
export function setWifiBandPreference(preferredBand: WifiBand) {
  return http.post('/api/network/wifi/band-preference', { preferredBand });
}
export function scanWifi(band: WifiBand | 'all' = 'all') {
  return http.get('/api/network/wifi/scan', { band });
}
export function configureWifi(profile: WifiProfile) {
  return http.post('/api/network/wifi/configure', profile);
}
