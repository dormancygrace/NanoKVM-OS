import { http } from '@/lib/http.ts';
import type { UsbComposition } from '@/lib/usb-composition.ts';

export const usbCompositionChangedEvent = 'nanokvm:usb-composition-changed';

// get virtual devices status
export function getVirtualDevice() {
  return http.get('/api/vm/device/virtual');
}

export async function setUsbComposition(composition: UsbComposition, revision: string) {
  const { keyboard, relative, absolute, network, disk, serial, audio, mode, pointerProfile } =
    composition;
  const response = await http.request({
    method: 'put',
    url: '/api/vm/device/virtual',
    data: {
      keyboard,
      relative,
      absolute,
      network,
      disk,
      serial,
      audio,
      mode,
      pointerProfile,
      revision
    }
  });
  if (response.code === 0) window.dispatchEvent(new Event(usbCompositionChangedEvent));
  return response;
}

export type UsbInternetStatus = {
  enabled: boolean;
  state: string;
  uplink?: string;
  address?: string;
  flowOffload: boolean;
  offloadReason?: string;
  ipv6: false;
};

export function getUsbInternet() {
  return http.get('/api/vm/usb-internet');
}

export function setUsbInternet(enabled: boolean, expectedEnabled: boolean) {
  return http.request({
    method: 'put',
    url: '/api/vm/usb-internet',
    data: { enabled, expectedEnabled }
  });
}
