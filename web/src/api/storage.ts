import { http } from '@/lib/http.ts';

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

export function getRemoteMedia() {
  return http.get('/api/storage/remote');
}
export function disconnectRemoteMedia() {
  return http.post('/api/storage/remote/disconnect');
}
