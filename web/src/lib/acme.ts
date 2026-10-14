// The ACME (acme.sh) status from GET /api/extensions/acme/status and the
// form logic of the certificate panel.
import type { CertificateInfo } from '@/lib/tls-certificate.ts';

export type AcmeRelease = { tag: string; checkedAt: string };

export type AcmeJob = {
  action: 'issue' | 'renew' | 'cron';
  startedAt: string;
  finishedAt?: string;
  ok: boolean;
  notDue?: boolean;
  error?: string;
  output?: string;
};

export type AcmeSettings = {
  domains: string[];
  email?: string;
  ca: string;
  challenge: 'http' | 'dns';
  dnsProvider?: string;
  dnsSleep?: number;
  issuedAt?: string;
};

export type AcmeStatus = {
  installed: boolean;
  version?: string;
  latest?: AcmeRelease;
  updateAvailable: boolean;
  settings?: AcmeSettings;
  certificate?: CertificateInfo;
  inUse: boolean;
  nextRenewal?: string;
  running: boolean;
  lastRun?: AcmeJob;
};

export type AcmeProviderOption = { name: string; description: string };

export type AcmeProvider = {
  hook: string;
  name: string;
  site?: string;
  docs?: string;
  options: AcmeProviderOption[];
  optionsAlt?: AcmeProviderOption[];
};

// The certificate authorities acme.sh knows by a short name; the test
// endpoints issue certificates browsers do not trust, for trying the set-up.
export const acmeAuthorities = [
  { value: 'letsencrypt', label: "Let's Encrypt" },
  { value: 'zerossl', label: 'ZeroSSL' },
  { value: 'buypass', label: 'Buypass' },
  { value: 'google', label: 'Google Trust Services' },
  { value: 'letsencrypt_test', label: "Let's Encrypt staging (test)" },
  { value: 'google_test', label: 'Google staging (test)' }
];

// Google registers accounts with an external account binding.
export const needsEab = (ca: string) => ca === 'google' || ca === 'google_test';

// ZeroSSL fetches the account credentials for an e-mail address.
export const needsEmail = (ca: string) => ca === 'zerossl';

// Domains typed as a list: commas, spaces or newlines between them.
export function parseDomains(text: string): string[] {
  const seen = new Set<string>();
  for (const part of text.split(/[\s,;]+/)) {
    const domain = part.trim().toLowerCase();
    if (domain && !seen.has(domain)) seen.add(domain);
  }
  return [...seen];
}

export const hasWildcard = (domains: string[]) => domains.some((d) => d.startsWith('*.'));

// The credential fields of a provider: the main set or the alternative one.
export function providerFields(provider: AcmeProvider | undefined, alt: boolean) {
  if (!provider) return [];
  if (alt && provider.optionsAlt?.length) return provider.optionsAlt;
  return provider.options;
}

type JobState = 'running' | 'ok' | 'notDue' | 'failed';

// What the last run came to, for the badge.
export function jobState(status: AcmeStatus): JobState | undefined {
  if (status.running) return 'running';
  const job = status.lastRun;
  if (!job) return undefined;
  if (!job.ok) return 'failed';
  return job.notDue ? 'notDue' : 'ok';
}

// Search in the provider list by name or hook.
export function providerMatches(provider: AcmeProvider, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return provider.name.toLowerCase().includes(q) || provider.hook.toLowerCase().includes(q);
}
