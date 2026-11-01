import assert from 'node:assert/strict';
import { test } from 'node:test';

import { certificateState, reloadAvailable } from '../src/lib/tls-certificate.ts';

const info = (overrides = {}) => ({
  subject: 'kvm.example',
  issuer: 'kvm.example',
  dnsNames: ['kvm.example'],
  ipAddresses: ['10.0.0.2'],
  notBefore: '2026-01-01T00:00:00Z',
  notAfter: '2027-01-01T00:00:00Z',
  expired: false,
  fingerprint: 'AA:BB',
  keyType: 'ECDSA P-256',
  selfSigned: true,
  chain: 1,
  ...overrides
});

const status = (overrides = {}) => ({
  enabled: true,
  custom: false,
  source: 'generated',
  certFile: '/etc/kvm/server.crt',
  keyFile: '/etc/kvm/server.key',
  certificate: info(),
  served: info(),
  pending: false,
  ...overrides
});

test('reload is offered whenever the served pair differs, even behind a boot error', () => {
  assert.equal(reloadAvailable(status()), false);
  assert.equal(reloadAvailable(status({ pending: true })), true);
  assert.equal(reloadAvailable(status({ enabled: false, pending: true })), false);
  // The custom pair failed at boot (the generated one is served, the error
  // is shown) and has been fixed on disk since: the badge says error, the
  // reload button must still be there.
  const fixed = status({ custom: true, pending: true, error: 'boot: custom.crt cannot be used' });
  assert.equal(certificateState(fixed), 'error');
  assert.equal(reloadAvailable(fixed), true);
});
