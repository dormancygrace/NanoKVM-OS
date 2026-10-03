export type UsbInputStatus = {
  available: boolean;
  keyboard: boolean;
  relative: boolean;
  absolute: boolean;
  pointerProfile: 'default' | 'windows';
};
export const noUsbInput: UsbInputStatus = {
  available: false,
  keyboard: false,
  relative: false,
  absolute: false,
  pointerProfile: 'default'
};
export function parseUsbInput(value: unknown): UsbInputStatus {
  if (!value || typeof value !== 'object') return noUsbInput;
  const data = value as Partial<UsbInputStatus>;
  const keyboard = data.keyboard === true;
  const relative = data.relative === true;
  const absolute = data.absolute === true;
  return {
    available: keyboard || relative || absolute,
    keyboard,
    relative,
    absolute,
    pointerProfile: data.pointerProfile === 'windows' ? 'windows' : 'default'
  };
}
export function availableMouseMode(preferred: string, status: UsbInputStatus) {
  if (preferred === 'relative' && status.relative) return 'relative';
  if (status.absolute) return 'absolute';
  return status.relative ? 'relative' : null;
}
