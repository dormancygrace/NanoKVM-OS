import { createLEDStream } from '@/lib/led-stream.ts';
import { subscribeLiveStatus } from '@/lib/live-status.ts';
import { getBaseUrl } from '@/lib/service.ts';

const leds = createLEDStream(
  () =>
    new EventSource(`${getBaseUrl('http')}/api/vm/gpio/events`, {
      withCredentials: (import.meta.env.VITE_WITH_CREDENTIALS as string) !== 'false'
    }),
  (listener) => subscribeLiveStatus((status) => listener(status?.gpio ?? null))
);

export const subscribeLEDStatus = leds.subscribe;
