import { atom } from 'jotai';

import type { RustDeskStatus } from '@/api/rustdesk';

export const rustDeskStatusAtom = atom<RustDeskStatus | null>(null);
