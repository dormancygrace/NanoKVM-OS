export const usbDevices = [
  'keyboard',
  'relative',
  'absolute',
  'network',
  'disk',
  'serial',
  'audio'
] as const;
export type UsbDevice = (typeof usbDevices)[number];
export type UsbComposition = Record<UsbDevice, boolean> & {
  mode: 'normal' | 'hid-only';
  pointerProfile?: 'default' | 'windows';
  identity?: UsbIdentity;
  // the writable FAT32 user disk, a third LUN of the virtual storage
  userDisk?: boolean;
};
export type EndpointCost = { in: number; out: number };
// What the connected computer sees in the device descriptor. The serial number
// is the chip UID and cannot be changed.
export type UsbIdentity = {
  vendorId: string;
  productId: string;
  manufacturer: string;
  product: string;
};
export type UsbStatus = UsbComposition & {
  hid: boolean;
  windowsPointerSupported?: boolean;
  revision?: string;
  budget: { inUsed: number; outUsed: number; inLimit: number; outLimit: number };
  costs: Record<UsbDevice, EndpointCost>;
  identity?: UsbIdentity & { serialNumber?: string; default?: boolean };
};

export const defaultUsbIdentity: UsbIdentity = {
  vendorId: '0x3346',
  productId: '0x1009',
  manufacturer: 'sipeed',
  product: 'NanoKVM'
};

// Identity presets: the factory one, and a neutral name for hosts whose
// endpoint security flags "NanoKVM". No preset carries another vendor's ID;
// a custom VID/PID is typed by hand.
export const usbIdentityPresets: { id: string; identity: UsbIdentity }[] = [
  { id: 'nanokvm', identity: defaultUsbIdentity },
  {
    id: 'generic',
    identity: { ...defaultUsbIdentity, manufacturer: 'Generic', product: 'USB Composite Device' }
  }
];

export function normalizeUsbId(value: string) {
  const match = /^\s*0[xX]([0-9a-fA-F]{1,4})\s*$/.exec(value);
  return match ? `0x${match[1].toLowerCase().padStart(4, '0')}` : value.trim();
}

export function sameIdentity(a?: UsbIdentity, b?: UsbIdentity) {
  const x = { ...defaultUsbIdentity, ...a };
  const y = { ...defaultUsbIdentity, ...b };
  return (
    normalizeUsbId(x.vendorId) === normalizeUsbId(y.vendorId) &&
    normalizeUsbId(x.productId) === normalizeUsbId(y.productId) &&
    x.manufacturer === y.manufacturer &&
    x.product === y.product
  );
}

export function matchingIdentityPreset(identity?: UsbIdentity) {
  return (
    usbIdentityPresets.find((preset) => sameIdentity(preset.identity, identity))?.id ?? 'custom'
  );
}

// Mirrors the server: 0x0001–0xffff, printable ASCII strings of 1–126 characters.
export function identityProblem(identity: UsbIdentity): 'id' | 'string' | undefined {
  for (const id of [identity.vendorId, identity.productId]) {
    const normalized = normalizeUsbId(id);
    if (!/^0x[0-9a-f]{4}$/.test(normalized) || normalized === '0x0000') return 'id';
  }
  for (const s of [identity.manufacturer, identity.product]) {
    if (
      s.length < 1 ||
      s.length > 126 ||
      !/^[\x21-\x7e][\x20-\x7e]*[\x21-\x7e]$|^[\x21-\x7e]$/.test(s)
    )
      return 'string';
  }
  return undefined;
}

const hid = { keyboard: true, relative: true, absolute: true };
const none = { network: false, disk: false, serial: false, audio: false };
/*
 * Starting points, in the order shown. The first one is what a fresh install
 * runs (three HID functions and the virtual disk without media), so a new
 * device starts on a preset. The budget is 6 IN / 7 OUT: everything at once
 * (HID + network + disk + serial = 8 IN) does not fit, so each preset gives
 * something up, and its description says what.
 */
export const usbPresets: { id: string; composition: UsbComposition }[] = [
  // the fresh-install composition; no network adapter appears on the computer
  { id: 'kvm', composition: { ...hid, ...none, disk: true, mode: 'normal' } },
  { id: 'standard', composition: { ...hid, ...none, network: true, disk: true, mode: 'normal' } },
  { id: 'console', composition: { ...hid, ...none, serial: true, disk: true, mode: 'normal' } },
  // a server managed over its serial login: the serial console and the
  // virtual disk, which carries the helper disk with the script that sets the
  // login up, so the server needs no network; no keyboard or mouse
  {
    id: 'headless',
    composition: {
      keyboard: false,
      relative: false,
      absolute: false,
      ...none,
      serial: true,
      disk: true,
      mode: 'normal'
    }
  },
  // what RustDesk needs: input and the speaker, so it matches a preset
  { id: 'remote', composition: { ...hid, ...none, disk: true, audio: true, mode: 'normal' } },
  { id: 'control', composition: { ...hid, ...none, mode: 'normal' } }
];

// The HID-only compatibility profile (USB 1.1 descriptors) has no network,
// disk or audio; it is a property of a composition, not a preset.
export function setCompatibilityProfile(
  composition: UsbComposition,
  enabled: boolean
): UsbComposition {
  if (!enabled) return { ...composition, mode: 'normal' };
  return { ...composition, mode: 'hid-only', network: false, disk: false, audio: false };
}

export function sameComposition(a: UsbComposition, b: UsbComposition) {
  return (
    a.mode === b.mode &&
    (a.pointerProfile ?? 'default') === (b.pointerProfile ?? 'default') &&
    sameIdentity(a.identity, b.identity) &&
    Boolean(a.userDisk) === Boolean(b.userDisk) &&
    usbDevices.every((name) => a[name] === b[name])
  );
}

export function matchingPreset(composition: UsbComposition) {
  return (
    usbPresets.find((preset) =>
      sameComposition(
        {
          ...preset.composition,
          pointerProfile: composition.pointerProfile,
          identity: composition.identity,
          userDisk: composition.userDisk
        },
        composition
      )
    )?.id ?? 'custom'
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
  const mode =
    enabled && (name === 'network' || name === 'disk' || name === 'audio')
      ? 'normal'
      : composition.mode;
  return { ...composition, mode, [name]: enabled };
}

export function canToggleDevice(composition: UsbComposition, name: UsbDevice, status: UsbStatus) {
  return composition[name] || fitsBudget(toggleDevice(composition, name), status);
}

export function normalizeUsbStatus(status: UsbStatus): UsbStatus {
  // Display older servers, but require a revision before applying a whole composition.
  return {
    ...status,
    audio: status.audio ?? false,
    keyboard: status.keyboard ?? status.hid,
    relative: status.relative ?? status.hid,
    absolute: status.absolute ?? status.hid,
    costs: {
      ...status.costs,
      audio: status.costs.audio ?? { in: 0, out: 1 },
      keyboard: status.costs.keyboard ?? { in: 1, out: 1 },
      relative: status.costs.relative ?? { in: 1, out: 1 },
      absolute: status.costs.absolute ?? { in: 1, out: 1 }
    }
  };
}
