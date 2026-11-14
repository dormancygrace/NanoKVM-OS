import { http } from '@/lib/http.ts';

import { DOWNLOAD_TIMEOUT } from './extensions/timeouts.ts';

export function uploadScript(formData: FormData) {
  return http.request<{ file: string; pythonMissing?: boolean }>({
    url: '/api/vm/script/upload',
    method: 'post',
    headers: {
      'Content-Type': 'multipart/form-data'
    },
    data: formData
  });
}

export function runScript(name: string, type: string) {
  return http.post<{ log: string }>('/api/vm/script/run', { name, type });
}

// Mark or unmark a script to run once at every startup.
export function setScriptAutostart(name: string, enabled: boolean) {
  return http.post<null>('/api/vm/script/autostart', { name, enabled });
}

// Python 3 for .py scripts: a package download.
export function installPython() {
  return http.post<null>('/api/vm/script/python', undefined, { timeout: DOWNLOAD_TIMEOUT });
}

// The answer to running a .py script without Python 3.
export const PYTHON_MISSING = -3;

export function getScripts() {
  return http.get<{
    files: string[];
    autostart: string[];
    autostartStatus: Record<string, string>;
  }>('/api/vm/script');
}

export function deleteScript(name: string) {
  const data = {
    name
  };
  return http.delete<null>('/api/vm/script', data);
}
