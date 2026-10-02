import { http } from '@/lib/http.ts';

// Names are free text in the UI; encode them so "#", "?" or "/" cannot change
// the request path.
const autostartPath = (name: string) => '/api/vm/autostart/' + encodeURIComponent(name);

export function getAutostart() {
  return http.get('/api/vm/autostart');
}

export function uploadAutostart(name: string, content: string) {
  return http.post(autostartPath(name), { content });
}

export function deleteAutostart(name: string) {
  return http.request({
    url: autostartPath(name),
    method: 'delete',
  });
}

export function getAutostartContent(name: string) {
    return http.get(autostartPath(name));
}
