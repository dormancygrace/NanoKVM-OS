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
  // IPv4 address (CIDR) now on eth0 or its VLAN; absent without one.
  currentAddress?: string;
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
  return http.get<{ interfaces: string[] }>('/api/network/wol/interfaces');
}

// get wake-on-lan macs history
export function getWolMacs() {
  return http.get<{ macs: string[] }>('/api/network/wol/mac');
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
  return http.get<WifiStatus>('/api/network/wifi');
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

export function getDNS() {
  return http.get('/api/network/dns');
}

export function setDNS(mode: DNSMode, servers: string[]) {
  return http.post('/api/network/dns', { mode, servers });
}

export type StunMode = 'default' | 'custom' | 'off';

export type WebRTCServers = {
  stunMode: StunMode;
  stun: string[];
  defaultStun: string[];
  turn: { address: string; username: string; hasCredential: boolean };
  dtlsCipher: DTLSCipher;
};

export type DTLSCipher = 'chacha20' | 'aes-gcm';

// The STUN and TURN servers of WebRTC video, audio and RustDesk.
export function getWebRTCServers() {
  return http.get<WebRTCServers>('/api/network/webrtc');
}

// credential: empty keeps the stored TURN password; an empty address removes TURN.
export function setWebRTCServers(body: {
  stunMode: StunMode;
  stun: string[];
  turn: { address: string; username: string; credential: string };
  dtlsCipher: DTLSCipher;
}) {
  return http.post('/api/network/webrtc', body);
}

export function getEthernet() {
  return http.get<EthernetConfig>('/api/network/ethernet');
}

export function setEthernet(config: Omit<EthernetConfig, 'interface' | 'adminUp' | 'linkUp'>) {
  return http.post<{ trial: NetworkTrial }>('/api/network/ethernet', config);
}

// A network change on trial: the device undoes it unless it is confirmed
// within expiresIn seconds from an address it can be reached at. hosts are
// where to look for it, the most likely first.
export type NetworkTrial = {
  id: string;
  token: string;
  expiresIn: number;
  hosts: string[];
};

export function confirmNetworkTrial(token: string, timeout?: number) {
  return http.post<null>(
    '/api/network/trial/confirm',
    { token },
    timeout ? { timeout } : undefined
  );
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
  return http.get<GatewayStatus>('/api/network/gateway');
}

export function setGatewayPreference(preferred: GatewayPreference) {
  return http.post<null>('/api/network/gateway', { preferred });
}

// Ethernet and Wi-Fi each have an IPv6 setting of their own: automatic
// (SLAAC and DHCPv6) or a static address, as for IPv4.
export type IPv6Mode = 'dhcpv6' | 'static';
export type IPv6Interface = {
  name: 'eth0' | 'wlan0';
  enabled: boolean;
  mode: IPv6Mode | '';
  // The static address with its prefix length, and the optional gateway.
  address: string;
  gateway: string;
  active: boolean;
  present: boolean;
};
export type IPv6Status = {
  enabled: boolean;
  active: boolean;
  supported: boolean;
  addresses: {
    interface: string;
    address: string;
    scope: 'global' | 'linkLocal' | 'private';
    mtu: number;
    // Where a global address of Ethernet or Wi-Fi came from.
    source?: 'slaac' | 'dhcpv6' | 'static';
  }[];
  // Missing on a server from before the per-interface setting.
  interfaces?: IPv6Interface[];
};

export function getIPv6() {
  return http.get<IPv6Status>('/api/network/ipv6');
}

export function setIPv6(
  iface: IPv6Interface['name'],
  setting: { enabled: boolean; mode?: IPv6Mode; address?: string; gateway?: string }
) {
  return http.post<null>('/api/network/ipv6', { ...setting, interface: iface });
}

export type WifiBand = '2.4' | '5';

// The administrator's access point, instead of joining a network. password
// is write-only: empty keeps the stored one. channel 0: chosen by a scan.
// subnet: three octets of a private /24, empty for the derived one.
export type WifiAP = {
  enabled: boolean;
  ssid: string;
  password?: string;
  band: WifiBand;
  channel: number;
  hidden: boolean;
  subnet: string;
  // Clients reach the internet through the device.
  internet: boolean;
  // DNS servers for its clients with internet (IPv4, up to three); empty:
  // the device's own.
  dns: string[];
};
export type WifiAPStatus = WifiAP & {
  passwordSet: boolean;
  running: boolean;
  address: string;
  stations: WifiAPStation[];
  // The device's DNS servers, which its clients get without a list.
  deviceDns: string[];
};
// A device on the access point; ip and hostname come with its lease.
export type WifiAPStation = {
  mac: string;
  ip?: string;
  hostname?: string;
  signal?: number;
  // Seconds.
  connected: number;
  // The last transmit rate to it, Mbit/s.
  rate?: number;
};

export function getWifiAP() {
  return http.get<WifiAPStatus>('/api/network/wifi/ap');
}

export function setWifiAP(ap: WifiAP) {
  // No trial when the request did not come over Wi-Fi.
  return http.post<{ trial?: NetworkTrial }>('/api/network/wifi/ap', ap);
}

export type WifiStatus = {
  supported: boolean;
  enabled: boolean;
  apMode: boolean;
  // The administrator's access point is up; Wi-Fi joins no network.
  accessPoint: boolean;
  connected: boolean;
  ssid: string;
  model: string;
  bands: WifiBand[];
  band: WifiBand | '';
  preferredBand: WifiBand;
  busy: boolean;
  error: string;
  connection?: WifiConnection;
  // The page reaches the device over Wi-Fi.
  thisConnection?: boolean;
};
// The current association; a value the driver does not report is missing.
export type WifiConnection = {
  frequency: number;
  channel?: number;
  width?: string;
  signal?: number;
  linkSpeed?: number;
  generation?: number;
  keyMgmt?: string;
  ip?: string;
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
  // Join again after a reboot or when it comes in range (default: yes).
  autoConnect?: boolean;
};
// A network the device has joined; passwords never leave it.
export type SavedWifiNetwork = {
  ssid: string;
  security: WifiSecurity | 'personal';
  hidden: boolean;
  autoConnect: boolean;
  current: boolean;
};
export function getSavedWifi() {
  return http.get<SavedWifiNetwork[]>('/api/network/wifi/saved');
}
export function connectSavedWifi(ssid: string) {
  return http.post('/api/network/wifi/saved/connect', { ssid });
}
export function forgetSavedWifi(ssid: string) {
  return http.post('/api/network/wifi/saved/forget', { ssid });
}
export function disconnectWifi() {
  return http.post('/api/network/wifi/disconnect');
}

export function setWifiEnabled(enabled: boolean) {
  return http.post('/api/network/wifi/enabled', { enabled });
}
export function setWifiBandPreference(preferredBand: WifiBand) {
  return http.post('/api/network/wifi/band-preference', { preferredBand });
}
export function scanWifi(band: WifiBand | 'all' = 'all') {
  return http.get<WifiNetwork[]>('/api/network/wifi/scan', { band });
}
export function configureWifi(profile: WifiProfile) {
  return http.post('/api/network/wifi/configure', profile);
}
