import { http } from '@/lib/http.ts';
import { inputLeaseHeaders } from '@/lib/input-lease.ts';

// paste
export function paste(content: string, langue: string) {
  return http.post('/api/hid/paste', { content, langue }, { headers: inputLeaseHeaders() });
}

// press a fixed key sequence on the host: Ctrl+Alt+Del, one SysRq key, REISUB
export function sendKeySequence(
  sequence: 'ctrl-alt-del' | 'sysrq' | 'reisub',
  options: { key?: string; delayMs?: number } = {}
) {
  return http.post<{ sequence: string; durationMs: number }>(
    '/api/hid/sequence',
    { sequence, ...options },
    { headers: inputLeaseHeaders() }
  );
}

// the running key sequence (REISUB goes on after its request) and the last result
export function getKeySequenceStatus() {
  return http.get('/api/hid/sequence');
}

// reset hid
export function reset() {
  return http.post('/api/hid/reset');
}

// get remote keyboard lock LED status
export function getKeyboardLedStatus() {
  return http.get('/api/hid/leds');
}

// get shortcuts
export function getShortcuts() {
  return http.get<{ shortcuts: Shortcut[] }>('/api/hid/shortcuts');
}

// add shortcut
export function addShortcut(keys: ShortcutKey[]) {
  const data = {
    keys
  };
  return http.post('/api/hid/shortcut', data);
}

// delete shortcut
export function deleteShortcut(id: string) {
  const data = {
    id
  };
  return http.delete('/api/hid/shortcut', data);
}

// get shortcut leader key
export function getLeaderKey() {
  return http.get<{ key: string }>('/api/hid/shortcut/leader-key');
}

// set shortcut leader key
export function setLeaderKey(key: string) {
  return http.post('/api/hid/shortcut/leader-key', { key });
}

export type ShortcutKey = { label: string; code: string };
export type Shortcut = { id: string; keys: ShortcutKey[] };
