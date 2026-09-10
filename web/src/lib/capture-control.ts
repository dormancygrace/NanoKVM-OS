import { getDefaultStore } from 'jotai';

import { getHdmiState, setHdmiState } from '@/api/vm.ts';
import { client } from '@/lib/websocket.ts';
import { captureBusyAtom, captureReadyAtom, isHdmiEnabledAtom } from '@/jotai/screen.ts';

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
  const rsp = await getHdmiState();
  if (current !== revision || store.get(captureBusyAtom)) return;
  if (rsp.code !== 0) throw new Error(rsp.msg);
  adopt(rsp.data.enabled);
}
export async function changeCapture(enabled: boolean) {
  if (store.get(captureBusyAtom)) return;
  ++revision;
  store.set(captureBusyAtom, true);
  // Release held controls before the device stops capture.
  if (!enabled) client.setInputEnabled(false);
  try {
    const rsp = await setHdmiState(enabled);
    if (rsp.code !== 0) throw new Error(rsp.msg);
    adopt(enabled);
  } finally {
    store.set(captureBusyAtom, false);
    // Read back even after an uncertain response; never replay the toggle.
    await refreshCapture().catch(() => undefined);
  }
}
