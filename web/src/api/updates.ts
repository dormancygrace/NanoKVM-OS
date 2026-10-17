import { http } from '@/lib/http';

export type PackageUpdate = { name: string; installed: string; available: string };

// The last look at the NanoKVM OS repository (server/osupdate/notice.go).
export type UpdateNotice = {
  enabled: boolean;
  state: 'never' | 'checking' | 'ok' | 'failed' | 'unavailable';
  checkedAt?: number;
  attemptAt?: number;
  message?: string;
  updates: PackageUpdate[];
};

export type Component = { name: string; installed: string; available?: string };

export type Components = {
  packages: Component[];
  alpine: string;
  kernel: string;
  bootKernel: string;
};

export const getUpdateNotice = () =>
  http.request({ method: 'get', url: '/api/os/update/notice', timeout: 10000 });

export const setUpdateNoticeEnabled = (enabled: boolean) =>
  http.request({ method: 'put', url: '/api/os/update/notice', data: { enabled } });

export const availableUpdates = (notice: UpdateNotice | null | undefined) =>
  notice?.enabled ? notice.updates : [];
