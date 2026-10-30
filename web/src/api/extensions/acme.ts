import type { AcmeProvider, AcmeStatus } from '@/lib/acme';
import { http } from '@/lib/http.ts';

import { SLOW_CALL_TIMEOUT } from './timeouts.ts';

// Install downloads the acme.sh release from GitHub over whatever uplink the
// device has; the call lasts as long as the download.
const DOWNLOAD_TIMEOUT = 5 * 60 * 1000;

export function getStatus() {
  return http.get<AcmeStatus>('/api/extensions/acme/status');
}

export function getProviders() {
  return http.get<AcmeProvider[]>('/api/extensions/acme/providers');
}

// Install or update to the latest stable release.
export function install() {
  return http.post('/api/extensions/acme/install', undefined, { timeout: DOWNLOAD_TIMEOUT });
}

export function checkUpdate() {
  return http.post<AcmeStatus>('/api/extensions/acme/check');
}

export function uninstall() {
  return http.post('/api/extensions/acme/uninstall', undefined, { timeout: SLOW_CALL_TIMEOUT });
}

type IssueRequest = {
  domains: string[];
  email: string;
  ca: string;
  challenge: 'http' | 'dns';
  dnsProvider?: string;
  dnsSleep?: number;
  dnsEnv: Record<string, string>;
  eabKid?: string;
  eabHmacKey?: string;
};

// Starts the issue job; poll getStatus for the result.
export function issue(req: IssueRequest) {
  return http.post('/api/extensions/acme/issue', req);
}

export function renew() {
  return http.post('/api/extensions/acme/renew');
}

export function remove() {
  return http.post('/api/extensions/acme/remove');
}
