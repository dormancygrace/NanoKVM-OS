export const usbDevices = [
  'keyboard',
  'relative',
  'absolute',
  'network',
  'disk',
  'serial'
] as const;
export type UsbDevice = (typeof usbDevices)[number];
export type UsbComposition = Record<UsbDevice, boolean> & { mode: 'normal' | 'hid-only' };
export type EndpointCost = { in: number; out: number };
export type UsbStatus = UsbComposition & {
  hid: boolean;
  revision?: string;
  budget: { inUsed: number; outUsed: number; inLimit: number; outLimit: number };
  costs: Record<UsbDevice, EndpointCost>;
};

const hid = { keyboard: true, relative: true, absolute: true };
export const usbPresets: { id: string; composition: UsbComposition }[] = [
  {
    id: 'standard',
    composition: { ...hid, network: true, disk: true, serial: false, mode: 'normal' }
  },
  {
    id: 'console',
    composition: { ...hid, network: false, disk: true, serial: true, mode: 'normal' }
  },
  {
    id: 'headless',
    composition: {
      keyboard: false,
      relative: false,
      absolute: false,
      network: true,
      disk: true,
      serial: true,
      mode: 'normal'
    }
  },
  {
    id: 'control',
    composition: { ...hid, network: false, disk: false, serial: false, mode: 'normal' }
  },
  {
    id: 'compatibility',
    composition: { ...hid, network: false, disk: false, serial: false, mode: 'hid-only' }
  }
];

export function sameComposition(a: UsbComposition, b: UsbComposition) {
  return a.mode === b.mode && usbDevices.every((name) => a[name] === b[name]);
}

export function matchingPreset(composition: UsbComposition) {
  return (
    usbPresets.find((preset) => sameComposition(preset.composition, composition))?.id ?? 'custom'
  );
}

export function endpointUsage(
  composition: UsbComposition,
  costs: UsbStatus['costs']
): EndpointCost {
  return usbDevices.reduce(
    (used, name) =>
      composition[name] ? { in: used.in + costs[name].in, out: used.out + costs[name].out } : used,
    { in: 0, out: 0 }
  );
}

export function fitsBudget(composition: UsbComposition, status: UsbStatus) {
  const used = endpointUsage(composition, status.costs);
  return used.in <= status.budget.inLimit && used.out <= status.budget.outLimit;
}

export function toggleDevice(composition: UsbComposition, name: UsbDevice): UsbComposition {
  const enabled = !composition[name];
  // Network/storage additions select the normal profile without a separate mode switch.
  const mode = enabled && (name === 'network' || name === 'disk') ? 'normal' : composition.mode;
  return { ...composition, mode, [name]: enabled };
}

export function canToggleDevice(composition: UsbComposition, name: UsbDevice, status: UsbStatus) {
  return composition[name] || fitsBudget(toggleDevice(composition, name), status);
}

export function normalizeUsbStatus(status: UsbStatus): UsbStatus {
  // Display older servers, but require a revision before applying a whole composition.
  return {
    ...status,
    keyboard: status.keyboard ?? status.hid,
    relative: status.relative ?? status.hid,
    absolute: status.absolute ?? status.hid,
    costs: {
      ...status.costs,
      keyboard: status.costs.keyboard ?? { in: 1, out: 1 },
      relative: status.costs.relative ?? { in: 1, out: 1 },
      absolute: status.costs.absolute ?? { in: 1, out: 1 }
    }
  };
}
