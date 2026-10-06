import { atom } from 'jotai';

import type { AddonInventory } from '@/api/addons';

export const addonInventoryAtom = atom<AddonInventory | null>(null);
