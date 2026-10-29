import { http } from '@/lib/http';

export type BmcStatus = {
  redfish: boolean;
  ipmi: boolean;
  ipmiInstalled: boolean;
  ipmiRunning: boolean;
  username: string;
  passwordSet: boolean;
  ipmiPort: number;
  holdSeconds: number;
  powerLedConnected: boolean;
};

export type BmcSettings = {
  redfish: boolean;
  ipmi: boolean;
  username: string;
  // Empty keeps the stored password.
  password: string;
  ipmiPort: number;
  holdSeconds: number;
  powerLedConnected: boolean;
};

export const getBmcStatus = () =>
  http.request<BmcStatus>({ method: 'get', url: '/api/bmc/status', timeout: 10000 });
export const configureBmc = (data: BmcSettings) =>
  http.request<BmcStatus>({ method: 'put', url: '/api/bmc/config', data, timeout: 30000 });
export const ipmiPackageAction = (action: 'install' | 'uninstall') =>
  http.post('/api/bmc/ipmi/' + action, undefined, { timeout: 190000 });
