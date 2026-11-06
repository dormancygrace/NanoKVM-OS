import { atxFromHardware } from '@/lib/atx.ts';
import { http } from '@/lib/http.ts';
import { createLiveStatus, type LiveStatus } from '@/lib/live-poll.ts';

export type { LiveStatus } from '@/lib/live-poll.ts';

const live = createLiveStatus(async () => {
  const rsp = await http.get<LiveStatus>('/api/vm/live-status');
  if (rsp.code !== 0) return null;
  const status = rsp.data;
  if (typeof status.atx === 'boolean') return status;

  // Older servers expose the ATX switch only through the hardware endpoint.
  // A failed fallback leaves ATX unknown without discarding the other states.
  try {
    const hardware = await http.get<{ atx?: boolean }>('/api/vm/hardware');
    if (hardware.code === 0) return { ...status, atx: atxFromHardware(hardware.data) };
  } catch {
    // Keep the last confirmed ATX value; the next poll retries the fallback.
  }
  return status;
});

export const subscribeLiveStatus = live.subscribe;
export const refreshLiveStatus = live.refresh;
