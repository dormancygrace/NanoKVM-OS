import { http } from '@/lib/http.ts';

import { SLOW_CALL_TIMEOUT } from './timeouts.ts';

// Settings > Services: LLDP, SNMP and UPS. Each is a package installed with the
// system and a service switched on and off; every call answers with the
// service's new status.

export type ServiceName = 'lldp' | 'snmp' | 'ups';

export type ServiceStatus = {
  installed: boolean;
  // starts at boot; it can be enabled and not running
  enabled: boolean;
  running: boolean;
  // the configuration was written by hand, not from these settings: it is
  // used as it is until it is replaced
  foreign: boolean;
};

export type LldpNeighbor = {
  interface: string;
  protocol: string;
  name: string;
  description: string;
  port: string;
  portDescription: string;
  address: string;
  age: string;
};

export type LldpStatus = ServiceStatus & {
  settings: { cdp: boolean };
  neighbors: LldpNeighbor[];
};

export type SnmpSettings = {
  community: string;
  // an address or a network (CIDR); empty is anyone
  network: string;
  location: string;
  contact: string;
};

export type SnmpStatus = ServiceStatus & { settings: SnmpSettings };

export type UpsStatus = ServiceStatus & {
  settings: {
    driver: string;
    // 'auto' for USB, or a serial port
    port: string;
    // USB only: the one device to use, 'vendor:product'; '' is the first UPS found
    usbDevice: string;
    network: boolean;
    user: string;
    password: string;
  };
  drivers: string[];
  // the USB port is in the host mode, which a USB UPS needs
  usbHost: boolean;
  // what the UPS reports; empty when the driver has no UPS
  values: Record<string, string>;
};

export type UpsSettingsRequest = {
  driver: string;
  port: string;
  usbDevice: string;
  network: boolean;
  newPassword?: boolean;
};

const url = (name: ServiceName, action: string) => `/api/extensions/${name}/${action}`;

export function getStatus<S extends ServiceStatus>(name: ServiceName) {
  return http.request<S>({ method: 'get', url: url(name, 'status'), timeout: 15000 });
}

export function setEnabled<S extends ServiceStatus>(name: ServiceName, enabled: boolean) {
  return http.request<S>({
    method: 'put',
    url: url(name, 'enabled'),
    data: { enabled },
    timeout: SLOW_CALL_TIMEOUT
  });
}

// Replaces the configuration, whoever wrote it, with the one of a fresh
// installation.
export function resetConfig<S extends ServiceStatus>(name: ServiceName) {
  return http.request<S>({ method: 'post', url: url(name, 'reset'), timeout: SLOW_CALL_TIMEOUT });
}

export function saveSettings<S extends ServiceStatus>(name: ServiceName, settings: unknown) {
  return http.request<S>({
    method: 'put',
    url: url(name, 'settings'),
    data: settings,
    timeout: SLOW_CALL_TIMEOUT
  });
}
