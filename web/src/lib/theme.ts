import * as storage from './localstorage.ts';
import type { Theme } from './theme-tokens.ts';

// The theme of this browser: dark unless the light one was chosen.
export function storedTheme(): Theme {
  return storage.getTheme() === 'light' ? 'light' : 'dark';
}

// On the root element, not inside the application: antd renders dialogs,
// menus and messages next to it in <body>.
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
}
