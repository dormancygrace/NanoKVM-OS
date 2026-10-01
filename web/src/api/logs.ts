import { http } from '@/lib/http.ts';

export type LogSource = 'system' | 'kernel' | 'application';
export type LogBoot = 'current' | 'previous' | 'older';
export type LogBootInfo = { id: LogBoot; startedAt: number; savedAt: number };
export type LogSnapshot = {
  source: LogSource;
  archiveAvailable: boolean;
  boot: LogBoot;
  state: 'ready' | 'empty' | 'unavailable';
  content: string;
  lines: number;
  truncated: boolean;
  collectedAt: number;
};

export function getLogs(source: LogSource, boot: LogBoot, signal: AbortSignal) {
  return http.request({
    method: 'get',
    url: '/api/vm/logs',
    params: { source, boot },
    signal,
    timeout: 10000
  });
}

export function getLogBoots(signal: AbortSignal) {
  return http.request({ method: 'get', url: '/api/vm/logs/boots', signal, timeout: 10000 });
}
