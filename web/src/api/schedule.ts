import { http } from '@/lib/http.ts';

// Scheduled tasks (Services > Scheduled tasks), kept in root's crontab.
export type ScheduledTask = {
  id: string;
  name: string;
  enabled: boolean;
  // Five cron fields, in the device's time zone.
  schedule: string;
  kind: 'script' | 'command';
  target: string;
};

export type TaskStatus = ScheduledTask & {
  next?: number[];
  // "running", "exited N", or absent before the first run since the boot.
  state?: string;
  started?: number;
  ended?: number;
};

export function getTasks() {
  return http.get<{
    tasks: TaskStatus[];
    other: string[];
    crond: boolean;
  }>('/api/vm/schedule');
}

export function saveTask(task: Omit<ScheduledTask, 'id'> & { id?: string }) {
  return http.post<{ id: string }>('/api/vm/schedule', task);
}

export function deleteTask(id: string) {
  return http.delete<null>('/api/vm/schedule', { id });
}

export function previewSchedule(schedule: string) {
  return http.post<{ next: number[] }>('/api/vm/schedule/preview', { schedule });
}

export function runTask(id: string) {
  return http.post<null>('/api/vm/schedule/run', { id });
}

export function getTaskLog(id: string) {
  return http.get<{ log: string }>(`/api/vm/schedule/log?id=${encodeURIComponent(id)}`);
}
