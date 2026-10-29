import { http } from '@/lib/http.ts';

import { DOWNLOAD_TIMEOUT, SLOW_CALL_TIMEOUT } from './timeouts.ts';

// The server runs apk and the Tailscale CLI with limits of up to a minute or
// more; the default 60 s transport limit reported failures that were not.

// install tailscale
export function install() {
  return http.post('/api/extensions/tailscale/install', undefined, { timeout: DOWNLOAD_TIMEOUT });
}

// uninstall tailscale
export function uninstall() {
  return http.post('/api/extensions/tailscale/uninstall', undefined, {
    timeout: SLOW_CALL_TIMEOUT
  });
}

// get tailscale status
export function getStatus() {
  return http.get<TailscaleStatus>('/api/extensions/tailscale/status');
}

// start tailscale
export function start() {
  return http.post('/api/extensions/tailscale/start', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

// restart tailscale
export function restart() {
  return http.post('/api/extensions/tailscale/restart', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

// stop tailscale
export function stop() {
  return http.post('/api/extensions/tailscale/stop', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

// run tailscale up
export function up() {
  return http.post('/api/extensions/tailscale/up', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

// run tailscale down
export function down() {
  return http.post('/api/extensions/tailscale/down', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

// login tailscale
export function login() {
  return http.post<{ url: string }>('/api/extensions/tailscale/login', undefined, {
    timeout: SLOW_CALL_TIMEOUT
  });
}

// logout tailscale
export function logout() {
  return http.post('/api/extensions/tailscale/logout', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

export type TailscaleState = 'notInstall' | 'notRunning' | 'notLogin' | 'stopped' | 'running';

export type TailscaleStatus = {
  state: TailscaleState;
  name: string;
  ip: string;
  account: string;
  busy?: boolean;
};
