import { atom } from 'jotai';

export type Branding = {
  style: 'connection' | 'screen' | 'custom';
  revision: string;
  customAvailable: boolean;
};
export const brandingAtom = atom<Branding>({
  style: 'connection',
  revision: '',
  customAvailable: false
});
export function brandingLogo(branding: Branding) {
  return branding.style === 'custom'
    ? `/api/branding/logo?v=${encodeURIComponent(branding.revision)}`
    : `/nanokvm-os-${branding.style}.svg?v=2`;
}
