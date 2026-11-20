import { atom } from 'jotai';

import type { Theme } from '@/lib/theme-tokens.ts';
import { storedThemeSetting, systemTheme, watchSystemTheme } from '@/lib/theme.ts';
import type { ThemeSetting } from '@/lib/theme.ts';

// menu bar disabled items
export const menuDisabledItemsAtom = atom<string[]>([]);

// track how many submenus are currently open
export const submenuOpenCountAtom = atom(0);

// signal all toolbar menus to close
export const menuCloseSignalAtom = atom(0);

// web title
export const webTitleAtom = atom('');

// the theme chosen in this browser
export const themeSettingAtom = atom<ThemeSetting>(storedThemeSetting());

// the theme of the device, followed while the interface is open
const systemThemeAtom = atom<Theme>(systemTheme());
systemThemeAtom.onMount = (set) => {
  set(systemTheme());
  return watchSystemTheme(set);
};

// the theme shown
export const themeAtom = atom<Theme>((get) => {
  const setting = get(themeSettingAtom);
  return setting === 'system' ? get(systemThemeAtom) : setting;
});

// menu display mode: 'off' | 'auto' | 'always'
export const menuDisplayModeAtom = atom<string>('auto');

// show the remote keyboard lock-status indicator beside the menu bar
export const keyboardLedStatusVisibleAtom = atom(true);

// show the host's power and disk LED indicator beside the menu bar
export const hostLedStatusVisibleAtom = atom(true);

export const settingsRequestAtom = atom<string | null>(null);

// The open tab of Services > Redfish and IPMI, kept while the desktop is open.
export const bmcTabAtom = atom('redfish');
