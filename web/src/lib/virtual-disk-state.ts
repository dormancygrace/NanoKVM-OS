import type { LiveStatus } from './live-poll.ts';

// Whether the applied USB composition has the virtual disk, from one live
// status. A failed poll (null) says nothing new and keeps what was known:
// turning it into "no disk" unmounts the Image menu and with it the
// browser-backed ISO the host is reading. Non-administrators get null.
export function virtualDiskFromLive(previous: boolean | null, live: LiveStatus | null) {
  if (live === null) return previous;
  const usb = live.usb;
  return typeof usb?.disk === 'boolean' ? usb.disk && usb.mode !== 'hid-only' : null;
}
