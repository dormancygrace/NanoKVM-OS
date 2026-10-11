// The HTTPS certificate status from GET /api/vm/tls.

export type CertificateInfo = {
  subject: string;
  issuer: string;
  dnsNames: string[];
  ipAddresses: string[];
  notBefore: string;
  notAfter: string;
  expired: boolean;
  fingerprint: string;
  keyType: string;
  selfSigned: boolean;
  chain: number;
};

export type TlsStatus = {
  enabled: boolean;
  custom: boolean;
  // generated (self-signed), custom (uploaded) or acme (issued by acme.sh).
  source: 'generated' | 'custom' | 'acme';
  certFile: string;
  keyFile: string;
  // The pair named by the configuration; absent with `error` when it cannot be read.
  certificate?: CertificateInfo;
  // The pair the listener presents while HTTPS is on.
  served?: CertificateInfo;
  // The configured pair is not the served one (a reload is due, or the
  // generated pair stands in for a broken custom one).
  pending: boolean;
  error?: string;
};

export type CertificateState = 'error' | 'pending' | 'expired' | 'acme' | 'custom' | 'generated';

// One word for the badge; the first that applies wins.
export function certificateState(status: TlsStatus): CertificateState {
  if (status.error) return 'error';
  if (status.enabled && status.pending) return 'pending';
  if (status.certificate?.expired) return 'expired';
  if (status.source === 'acme') return 'acme';
  return status.custom ? 'custom' : 'generated';
}

export const certificateTone: Record<
  CertificateState,
  'success' | 'warning' | 'danger' | 'neutral'
> = {
  error: 'danger',
  pending: 'warning',
  expired: 'danger',
  acme: 'success',
  custom: 'success',
  generated: 'neutral'
};

// The pair on disk can be served without a restart: HTTPS is on and the
// listener presents another pair (after a renewal by another tool, or when
// the generated pair stood in at boot and the custom one has been fixed
// since). Independent of the badge, which may show the boot error.
export function reloadAvailable(status: TlsStatus): boolean {
  return status.enabled && status.pending;
}

// The names the certificate is valid for, DNS names first.
export function certificateNames(info: CertificateInfo): string[] {
  return [...info.dnsNames, ...info.ipAddresses];
}
