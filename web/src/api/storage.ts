import { http } from '@/lib/http.ts';
import { getBaseUrl } from '@/lib/service.ts';

// get image list
export function getImages() {
  return http.get('/api/storage/image');
}

// get mounted image
export function getMountedImage() {
  return http.get('/api/storage/image/mounted');
}

// mount/unmount image
export function mountImage(file?: string, cdrom?: boolean, force = false) {
  const data = {
    file: file ? file : '',
    cdrom: cdrom,
    force
  };
  return http.post('/api/storage/image/mount', data);
}

// get CD-ROM flag
export function getCdRom() {
  return http.get('/api/storage/cdrom');
}

export function deleteImage(file: string) {
  const data = {
    file
  };
  return http.post('/api/storage/image/delete', data);
}

// The writable FAT32 user disk (a third LUN of the virtual storage).
export function getUserDisk() {
  return http.get('/api/storage/user-disk');
}
export function createUserDisk(sizeMiB: number) {
  return http.post('/api/storage/user-disk/create', { sizeMiB });
}
export function deleteUserDisk() {
  return http.post('/api/storage/user-disk/delete');
}

// Network boot: the official iPXE image on the virtual disk loads the
// netboot.xyz menus over USB internet sharing.
export type NetbootStatus = {
  enabled: boolean;
  secureBoot: boolean;
  ipxe: string;
  netbootxyz: string;
  checkedAt: string;
  menu?: string;
  menuFetches: number;
  lastFetch?: number;
  usbNetwork: boolean;
  virtualDisk: boolean;
  usbInternet: { enabled: boolean; state: string; uplink?: string };
  browserMedia: boolean;
};
// Downloads of the iPXE images (about 13 MB) can take minutes on Wi-Fi.
const netbootTimeout = 11 * 60 * 1000;
export function getNetboot() {
  return http.get('/api/storage/netboot');
}
export function updateNetboot() {
  return http.post('/api/storage/netboot/update', undefined, { timeout: netbootTimeout });
}
export function enableNetboot(secureBoot: boolean) {
  return http.post('/api/storage/netboot/enable', { secureBoot }, { timeout: netbootTimeout });
}
export function disableNetboot() {
  return http.post('/api/storage/netboot/disable');
}

export type RemoteMediaStatus = {
  connected: boolean;
  source?: 'browser' | 'http' | 'smb';
  mounted?: boolean;
  name?: string;
  size?: number;
  readBytes?: number;
  error?: string;
};
export function connectHTTPImage(options: { url: string; username: string; password: string }) {
  return http.post('/api/storage/remote/http', options, { sensitive: true });
}
export function connectSMBImage(options: {
  url: string;
  username: string;
  password: string;
  domain: string;
}) {
  return http.post('/api/storage/remote/smb', options, { sensitive: true });
}

export function getRemoteMedia() {
  return http.get('/api/storage/remote');
}
export function disconnectRemoteMedia() {
  return http.post('/api/storage/remote/disconnect');
}

// Paths are relative to the dedicated FAT32 user disk, never the SD data root.
const userFilesUrl = '/api/storage/user-disk/files';
export type UserDiskEntry = {
  name: string;
  directory: boolean;
  size: number;
  modifiedAt: string;
};
export type UserDiskListing = {
  path: string;
  entries: UserDiskEntry[];
  offset: number;
  next: boolean;
  free: number;
  total: number;
};
export function listUserDiskFiles(path: string, offset = 0) {
  return http.get(userFilesUrl, { path, offset });
}
export function uploadUserDiskFile(
  path: string,
  file: File,
  signal: AbortSignal,
  progress: (percent: number) => void
) {
  return http.post(userFilesUrl + '/upload', file, {
    params: { path },
    signal,
    timeout: 0,
    headers: { 'Content-Type': 'application/octet-stream' },
    onUploadProgress: ({ loaded, total }) =>
      progress(total ? Math.floor((loaded * 100) / total) : 0)
  });
}
export function makeUserDiskDirectory(path: string) {
  return http.post(userFilesUrl + '/mkdir', null, { params: { path } });
}
export function deleteUserDiskFile(path: string) {
  return http.post(userFilesUrl + '/delete', null, { params: { path } });
}
export function userDiskDownloadUrl(path: string) {
  return getBaseUrl('http') + userFilesUrl + '/download?' + new URLSearchParams({ path });
}
