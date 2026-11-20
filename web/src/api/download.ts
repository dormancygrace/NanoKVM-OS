import { http } from '@/lib/http.ts';

// Download image
export function downloadImage(file?: string, sha256sum?: string) {
  const data = {
    file: file ?? '',
    sha256sum: sha256sum ?? ''
  };
  return http.post('/api/download/image', data);
}

export async function checkImageUpload(file: File) {
  const result = await http.get('/api/download/file/check', { name: file.name, size: file.size });
  if (result.code !== 0) throw new Error(result.msg);
}

export function cancelDownloadImage() {
  return http.post('/api/download/image/cancel');
}

export function statusImage() {
  return http.get<ImageDownloadStatus>('/api/download/image/status');
}

export function imageEnabled() {
  return http.get<{ enabled: boolean }>('/api/download/image/enabled');
}

type ImageDownloadStatus = {
  downloadedBytes: number;
  totalBytes: number;
  bytesPerSecond: number;
  status: string;
  file: string;
  percentage: string;
};
