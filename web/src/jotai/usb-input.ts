import { atom } from 'jotai';

import { noUsbInput } from '@/lib/usb-input.ts';

export const usbInputAtom = atom(noUsbInput);
