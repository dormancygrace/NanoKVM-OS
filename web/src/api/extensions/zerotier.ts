import { http } from '@/lib/http.ts';

import { DOWNLOAD_TIMEOUT, SLOW_CALL_TIMEOUT } from './timeouts.ts';

// ZeroTier is the nanokvm-zerotier add-on (about 1 MB). Its CLI answers
// quickly, but start and stop go through OpenRC. A transport timeout is
// treated by the UI as an unknown result. `http.get` takes no per-request
// config, so status goes through `http.request`.

export function install() {
  return http.post('/api/extensions/zerotier/install', undefined, {
    timeout: DOWNLOAD_TIMEOUT
  });
}

// purge: also delete the identity (the node address) and the joined networks.
export function uninstall(purge = false) {
  return http.post('/api/extensions/zerotier/uninstall', { purge }, { timeout: SLOW_CALL_TIMEOUT });
}

export function getStatus() {
  return http.request<ZerotierStatus>({
    method: 'get',
    url: '/api/extensions/zerotier/status',
    timeout: SLOW_CALL_TIMEOUT
  });
}

export function start() {
  return http.post('/api/extensions/zerotier/start', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

export function restart() {
  return http.post('/api/extensions/zerotier/restart', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

export function stop() {
  return http.post('/api/extensions/zerotier/stop', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

export function join(network: string) {
  return http.post('/api/extensions/zerotier/join', { network }, { timeout: SLOW_CALL_TIMEOUT });
}

export function leave(network: string) {
  return http.post('/api/extensions/zerotier/leave', { network }, { timeout: SLOW_CALL_TIMEOUT });
}

// A network ID: the controller's 10-digit address and a 6-digit network number.
export const networkIdPattern = /^[0-9a-f]{16}$/;

export type ZerotierState = 'notInstall' | 'notRunning' | 'connecting' | 'running';

export type ZerotierNetwork = {
  id: string;
  name: string;
  // OK, REQUESTING_CONFIGURATION, ACCESS_DENIED, NOT_FOUND, PORT_ERROR,
  // CLIENT_TOO_OLD or AUTHENTICATION_REQUIRED.
  status: string;
  type: string;
  device: string;
  addresses: string[];
};

export type ZerotierStatus = {
  state: ZerotierState;
  address?: string;
  ip?: string;
  version?: string;
  networks?: ZerotierNetwork[];
};
