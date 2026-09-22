import { atom } from 'jotai';

export type Branding = {
  logoRevision: string;
  faviconRevision: string;
  customLogoAvailable: boolean;
  customFaviconAvailable: boolean;
  buttonColor?: string;
  customButtonColor?: boolean;
  bannerStyle?: 'default' | 'rainbow';
};
export const DEFAULT_BUTTON_COLOR = '#45E9A0';
export const brandingAtom = atom<Branding>({
  logoRevision: '',
  faviconRevision: '',
  customLogoAvailable: false,
  customFaviconAvailable: false,
  buttonColor: DEFAULT_BUTTON_COLOR,
  customButtonColor: false,
  bannerStyle: 'default'
});
export function brandingButtonColor(branding: Branding) {
  const color = branding.buttonColor?.trim().toUpperCase();
  return color && /^#[0-9A-F]{6}$/.test(color) ? color : DEFAULT_BUTTON_COLOR;
}
export function buttonTextColor(color: string) {
  const red = Number.parseInt(color.slice(1, 3), 16);
  const green = Number.parseInt(color.slice(3, 5), 16);
  const blue = Number.parseInt(color.slice(5, 7), 16);
  return (red * 299 + green * 587 + blue * 114) / 1000 >= 160 ? '#102119' : '#FFFFFF';
}
export function brandingLogo(branding: Branding) {
  return branding.customLogoAvailable
    ? `/api/branding/logo?v=${encodeURIComponent(branding.logoRevision)}`
    : '/nanokvm-os-connection-full.svg?v=2';
}
export function brandingFavicon(branding: Branding) {
  return branding.customFaviconAvailable
    ? `/api/branding/favicon?v=${encodeURIComponent(branding.faviconRevision)}`
    : '/nanokvm-os-connection.svg?v=3';
}
