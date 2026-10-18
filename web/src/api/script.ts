import { http } from '@/lib/http.ts';

export function uploadScript(formData: FormData) {
  return http.request({
    url: '/api/vm/script/upload',
    method: 'post',
    headers: {
      'Content-Type': 'multipart/form-data'
    },
    data: formData
  });
}

export function runScript(name: string, type: string) {
  return http.post('/api/vm/script/run', { name, type });
}

// Mark or unmark a script to run once at every startup.
export function setScriptAutostart(name: string, enabled: boolean) {
  return http.post('/api/vm/script/autostart', { name, enabled });
}

export function getScripts() {
  return http.get('/api/vm/script');
}

export function deleteScript(name: string) {
  const data = {
    name
  };
  return http.delete('/api/vm/script', data);
}
