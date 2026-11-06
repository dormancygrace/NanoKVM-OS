import { atom } from 'jotai';

// ATX power control is on (System > Device): the toolbar shows the power
// button. null until the live status answered.
export const atxEnabledAtom = atom<boolean | null>(null);

// performance.now() of the last switch in this tab: a live status requested
// before it may predate the change and is not applied.
export const atxLocalChange = { at: Number.NEGATIVE_INFINITY };
