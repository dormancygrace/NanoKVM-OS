import { getDefaultStore } from 'jotai';

import { getHdmiState, setHdmiState } from '@/api/vm.ts';
import type { LiveStatus } from '@/lib/live-status.ts';
import { client } from '@/lib/websocket.ts';
import {
  captureBusyAtom,
  captureReadyAtom,
  isHdmiEnabledAtom,
  videoSessionCountAtom
} from '@/jotai/screen.ts';

const store = getDefaultStore();
let revision = 0;
function adopt(enabled: boolean) {
  client.setInputEnabled(enabled);
  if (!enabled && document.pointerLockElement) document.exitPointerLock();
  store.set(isHdmiEnabledAtom, enabled);
  store.set(captureReadyAtom, true);
}
export async function refreshCapture() {
  if (store.get(captureBusyAtom)) return;
  const current = ++revision;
  let rsp;
  try {
    rsp = await getHdmiState();
  } catch (error) {
    if (current === revision) store.set(videoSessionCountAtom, null);
    throw error;
  }
  if (current !== revision || store.get(captureBusyAtom)) return;
  if (rsp.code !== 0) {
    store.set(videoSessionCountAtom, null);
    throw new Error(rsp.msg);
  }
  const count = rsp.data.viewerCount;
  store.set(videoSessionCountAtom, Number.isSafeInteger(count) && count >= 0 ? count : null);
  adopt(rsp.data.enabled);
}
// The shared live poll; data requested before the last change is stale.
let changedAt = -Infinity;
export function adoptLiveCapture(hdmi: LiveStatus['hdmi'] | null, startedAt: number) {
  if (store.get(captureBusyAtom) || startedAt < changedAt) return;
  if (!hdmi) {
    store.set(videoSessionCountAtom, null);
    return;
  }
  const count = hdmi.viewerCount;
  store.set(videoSessionCountAtom, Number.isSafeInteger(count) && count >= 0 ? count : null);
  adopt(hdmi.enabled);
}
export async function changeCapture(enabled: boolean) {
  if (store.get(captureBusyAtom)) return;
  ++revision;
  changedAt = performance.now();
  store.set(captureBusyAtom, true);
  // Release held controls before the device stops capture.
  if (!enabled) client.setInputEnabled(false);
  try {
    const rsp = await setHdmiState(enabled);
    if (rsp.code !== 0) throw new Error(rsp.msg);
    adopt(enabled);
  } finally {
    changedAt = performance.now();
    store.set(captureBusyAtom, false);
    // Read back even after an uncertain response; never replay the toggle.
    await refreshCapture().catch(() => undefined);
  }
}
