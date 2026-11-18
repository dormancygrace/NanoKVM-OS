import { http } from '@/lib/http.ts';
import type { UsbComposition, UsbPortPower, UsbRole, UsbStatus } from '@/lib/usb-composition.ts';

export const usbCompositionChangedEvent = 'nanokvm:usb-composition-changed';

// get virtual devices status
export function getVirtualDevice() {
  return http.get<UsbStatus>('/api/vm/device/virtual');
}

export async function setUsbComposition(composition: UsbComposition, revision: string) {
  const {
    keyboard,
    relative,
    absolute,
    network,
    disk,
    serial,
    audio,
    mode,
    identity,
    storageIdentity,
    userDisk
  } = composition;
  const response = await http.request<UsbStatus>({
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
      revision,
      // omitted keeps the identity and the user disk in place on the server
      ...(identity ? { identity } : {}),
      ...(storageIdentity ? { storageIdentity } : {}),
      ...(userDisk === undefined ? {} : { userDisk })
    }
  });
  if (response.code === 0) window.dispatchEvent(new Event(usbCompositionChangedEvent));
  return response;
}

// What the USB port is: gadget, host or off. The answer is the new status.
export async function setUsbRole(role: UsbRole) {
  const response = await http.request<UsbStatus>({
    method: 'put',
    url: '/api/vm/usb/role',
    data: { role }
  });
  if (response.code === 0) window.dispatchEvent(new Event(usbCompositionChangedEvent));
  return response;
}

// A device attached to the NanoKVM while its port is a host.
export type UsbHostDevice = {
  port: string;
  vendorId: string;
  productId: string;
  manufacturer: string;
  product: string;
  // Mbit/s: 1.5, 12, 480
  speed: string;
  class: string;
  interfaceClasses: string[] | null;
  maxPower: string;
};
export type UsbHostDevices = {
  devices: UsbHostDevice[];
  portPower: UsbPortPower;
};

export function getUsbHostDevices() {
  return http.get<UsbHostDevices>('/api/vm/usb/devices');
}

// The headless-Linux console helper: serial number, script and helper disk.
export function getSerialConsole() {
  return http.get('/api/vm/serial-console');
}

// Sends Enter on the NanoKVM's serial port and reports what the computer answered.
export function testSerialConsole() {
  return http.post('/api/vm/serial-console/test');
}

export type UsbInternetStatus = {
  enabled: boolean;
  state: string;
  uplink?: string;
  address?: string;
  flowOffload: boolean;
  offloadReason?: string;
  ipv6: false;
  // The computer may open every service over usb0 (the input filter is off).
  hostAccess: boolean;
};

export function getUsbInternet() {
  return http.get<UsbInternetStatus>('/api/vm/usb-internet');
}

export function setUsbInternet(enabled: boolean, expectedEnabled: boolean) {
  return http.request<UsbInternetStatus>({
    method: 'put',
    url: '/api/vm/usb-internet',
    data: { enabled, expectedEnabled }
  });
}

export function setUsbHostAccess(enabled: boolean, expectedEnabled: boolean) {
  return http.request<UsbInternetStatus>({
    method: 'put',
    url: '/api/vm/usb-host-access',
    data: { enabled, expectedEnabled }
  });
}
