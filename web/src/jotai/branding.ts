import { atom } from 'jotai';

import type { Theme } from '@/lib/theme-tokens.ts';

export type Branding = {
  title?: string;
  logoRevision: string;
  faviconRevision: string;
  customLogoAvailable: boolean;
  customFaviconAvailable: boolean;
  buttonColor?: string;
  customButtonColor?: boolean;
  bannerStyle?: 'default' | 'rainbow';
  // What opens after the sign-in. Unknown until the device has answered.
  startPage?: 'overview' | 'screen';
};
const DEFAULT_BUTTON_COLOR = '#45E9A0';
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
// The button colour as the light theme uses it. There it also draws on white:
// links, the selected tab, a switch, and carries white text on a button. A
// pale colour is darkened until those read (4.5:1 against white), its hue kept.
export function colorOnLight(color: string) {
  let channels = [1, 3, 5].map((at) => Number.parseInt(color.slice(at, at + 2), 16));
  const luminance = () => {
    const [red, green, blue] = channels.map((value) => {
      const part = value / 255;
      return part <= 0.04045 ? part / 12.92 : ((part + 0.055) / 1.055) ** 2.4;
    });
    return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
  };
  while (luminance() > 0.183) {
    channels = channels.map((value) => Math.floor(value * 0.95));
  }
  return (
    '#' +
    channels
      .map((value) => value.toString(16).padStart(2, '0'))
      .join('')
      .toUpperCase()
  );
}
export function buttonTextColor(color: string) {
  const red = Number.parseInt(color.slice(1, 3), 16);
  const green = Number.parseInt(color.slice(3, 5), 16);
  const blue = Number.parseInt(color.slice(5, 7), 16);
  return (red * 299 + green * 587 + blue * 114) / 1000 >= 160 ? '#102119' : '#FFFFFF';
}
// The built-in logo has its name in a colour for each theme; an uploaded one
// is shown as it is.
export function builtinLogo(theme: Theme) {
  return theme === 'light'
    ? '/nanokvm-os-connection-full-light.svg'
    : '/nanokvm-os-connection-full.svg?v=2';
}
export function brandingLogo(branding: Branding, theme: Theme) {
  return branding.customLogoAvailable
    ? `/api/branding/logo?v=${encodeURIComponent(branding.logoRevision)}`
    : builtinLogo(theme);
}
export function brandingFavicon(branding: Branding) {
  return branding.customFaviconAvailable
    ? `/api/branding/favicon?v=${encodeURIComponent(branding.faviconRevision)}`
    : '/nanokvm-os-connection.svg?v=3';
}
