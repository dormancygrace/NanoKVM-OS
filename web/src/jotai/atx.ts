import { atom } from 'jotai';

// ATX power control is on (System > Device): the toolbar shows the power
// button. null until /api/vm/hardware answered.
export const atxEnabledAtom = atom<boolean | null>(null);
