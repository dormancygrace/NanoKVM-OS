import { http } from '@/lib/http.ts';
import type { UsbComposition } from '@/lib/usb-composition.ts';

export const usbCompositionChangedEvent = 'nanokvm:usb-composition-changed';

// get virtual devices status
export function getVirtualDevice() {
  return http.get('/api/vm/device/virtual');
}

export async function setUsbComposition(composition: UsbComposition, revision: string) {
  const { keyboard, relative, absolute, network, disk, serial, mode } = composition;
  const response = await http.request({
    method: 'put',
    url: '/api/vm/device/virtual',
    data: { keyboard, relative, absolute, network, disk, serial, mode, revision }
  });
  if (response.code === 0) window.dispatchEvent(new Event(usbCompositionChangedEvent));
  return response;
}

// mount/unmount virtual device
export async function updateVirtualDevice(device: string) {
  const data = {
    device
  };

  const response = await http.post('/api/vm/device/virtual', data);
  if (response.code === 0) window.dispatchEvent(new Event(usbCompositionChangedEvent));
  return response;
}
