import { http } from '@/lib/http';

export type BmcStatus = {
  redfish: boolean;
  ipmi: boolean;
  ipmiRunning: boolean;
  // What the running IPMI daemon reports; absent while it does not run.
  ipmiDaemon?: IpmiDaemonStatus;
  username: string;
  passwordSet: boolean;
  ipmiPort: number;
  holdSeconds: number;
  powerLedConnected: boolean;
  virtualMedia: boolean;
};

export type IpmiDaemonStatus = {
  port: number;
  sessions: number;
  // Clients refused for too many logins.
  lockedOut: number;
  // The last power command accepted ("power on", ...); empty before the first.
  lastCommand: string;
  // RFC 3339; absent before the first power command.
  lastCommandAt?: string;
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
  virtualMedia: boolean;
};

export const getBmcStatus = () =>
  http.request<BmcStatus>({ method: 'get', url: '/api/bmc/status', timeout: 10000 });
export const configureBmc = (data: BmcSettings) =>
  http.request<null>({
    method: 'put',
    url: '/api/bmc/config',
    data,
    timeout: 30000,
    sensitive: true
  });
