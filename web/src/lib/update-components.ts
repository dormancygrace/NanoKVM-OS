import type { TFunction } from 'i18next';

import type { PackageUpdate } from '@/api/updates.ts';

// Package names as people know the parts of NanoKVM OS.
const labels: Record<string, string> = {
  nanokvm: 'system',
  'nanokvm-release': 'release',
  'nanokvm-app': 'app',
  'nanokvm-web': 'web',
  'nanokvm-base': 'base',
  'nanokvm-kernel-sg2002': 'kernel',
  'nanokvm-kmod-sg2002': 'modules',
  'nanokvm-firmware-sg2002': 'firmware',
  'nanokvm-keys': 'keys',
  'nanokvm-rustdesk': 'rustdesk',
  'nanokvm-bootloader-sg2002': 'bootloader',
  'nanokvm-zlib': 'zlib',
  'nanokvm-openssl': 'openssl',
  'nanokvm-exfatprogs': 'exfatprogs',
  'nanokvm-i2c-tools': 'i2ctools'
};

export function componentLabel(t: TFunction, name: string): string {
  const key = labels[name];
  return key ? t(`settings.updates.components.names.${key}`) : name;
}

// The notification shows once per set of available versions in a browser.
const SEEN_KEY = 'nano-kvm-update-notice-seen';

export function updateSignature(updates: PackageUpdate[]): string {
  return updates
    .map((update) => `${update.name}@${update.available}`)
    .sort()
    .join(' ');
}

export function getSeenUpdates(): string | null {
  try {
    return localStorage.getItem(SEEN_KEY);
  } catch {
    return null;
  }
}

export function setSeenUpdates(signature: string) {
  try {
    localStorage.setItem(SEEN_KEY, signature);
  } catch {
    /* A private window: the notice may show again, the badge stays. */
  }
}
