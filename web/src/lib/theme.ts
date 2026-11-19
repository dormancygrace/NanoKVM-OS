import * as storage from './localstorage.ts';
import type { Theme } from './theme-tokens.ts';

// What is chosen in this browser: one of the themes, or the one the device
// asks for.
export type ThemeSetting = Theme | 'system';

const systemLight = '(prefers-color-scheme: light)';

// The device's theme unless one was chosen.
export function storedThemeSetting(): ThemeSetting {
  const value = storage.getTheme();
  return value === 'light' || value === 'dark' ? value : 'system';
}

export function systemTheme(): Theme {
  return window.matchMedia(systemLight).matches ? 'light' : 'dark';
}

// Calls back when the device changes its theme; returns the unsubscribe.
export function watchSystemTheme(onChange: (theme: Theme) => void) {
  const query = window.matchMedia(systemLight);
  const listener = () => onChange(query.matches ? 'light' : 'dark');
  query.addEventListener('change', listener);
  return () => query.removeEventListener('change', listener);
}

// The theme to show now.
export function storedTheme(): Theme {
  const setting = storedThemeSetting();
  return setting === 'system' ? systemTheme() : setting;
}

// On the root element, not inside the application: antd renders dialogs,
// menus and messages next to it in <body>.
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = theme;
}
