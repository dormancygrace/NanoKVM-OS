import { http } from '@/lib/http.ts';

// Install downloads the acme.sh release from GitHub over whatever uplink the
// device has; the call lasts as long as the download.
const DOWNLOAD_TIMEOUT = 5 * 60 * 1000;

export function getStatus() {
  return http.get('/api/extensions/acme/status');
}

export function getProviders() {
  return http.get('/api/extensions/acme/providers');
}

// Install or update to the latest stable release.
export function install() {
  return http.post('/api/extensions/acme/install', undefined, { timeout: DOWNLOAD_TIMEOUT });
}

export function checkUpdate() {
  return http.post('/api/extensions/acme/check');
}

export function uninstall() {
  return http.post('/api/extensions/acme/uninstall');
}

export type IssueRequest = {
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
