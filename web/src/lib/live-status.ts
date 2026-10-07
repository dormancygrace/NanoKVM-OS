import { http } from '@/lib/http.ts';
import { createLiveStatus, type LiveStatus } from '@/lib/live-poll.ts';

export type { LiveStatus } from '@/lib/live-poll.ts';

const live = createLiveStatus(async () => {
  const rsp = await http.get('/api/vm/live-status');
  return rsp.code === 0 ? (rsp.data as LiveStatus) : null;
});

export const subscribeLiveStatus = live.subscribe;
export const refreshLiveStatus = live.refresh;
