import { atom } from 'jotai';

import type { UpdateNotice } from '@/api/updates.ts';

// Filled by the update notification (admins) and the Updates page.
export const updateNoticeAtom = atom<UpdateNotice | null>(null);
