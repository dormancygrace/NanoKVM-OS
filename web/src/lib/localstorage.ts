import type { DirectPlayback } from './video-model.ts';

const LANGUAGE_KEY = 'nano-kvm-language';
const VIDEO_MODE_KEY = 'nano-kvm-vide-mode';
const VIDEO_SCALE_KEY = 'nano-kvm-video-scale';
const MOUSE_STYLE_KEY = 'nano-kvm-mouse-style';
const MOUSE_MODE_KEY = 'nano-kvm-mouse-mode';
const INPUT_ADAPTER_KEY = 'nano-kvm-input-adapter';
const MOUSE_SCROLL_DIRECTION_KEY = 'nano-kvm-mouse-scroll-direction';
const MOUSE_SCROLL_INTERVAL_KEY = 'nano-kvm-mouse-scroll-interval';
const KEYBOARD_SYSTEM_KEY = 'nano-kvm-keyboard-system';
const KEYBOARD_LANGUAGE_KEY = 'nano-kvm-keyboard-language';
const MENU_DISABLED_ITEMS_KEY = 'nano-kvm-menu-disabled-items';
const MENU_AUTO_HIDE_KEY = 'nano-kvm-menu-auto-hide';
const THEME_KEY = 'nano-kvm-theme';
const KEYBOARD_LED_STATUS_VISIBLE_KEY = 'nano-kvm-keyboard-led-status-visible';
const HOST_LED_STATUS_VISIBLE_KEY = 'nano-kvm-host-led-status-visible';

export function getLanguage() {
  return localStorage.getItem(LANGUAGE_KEY);
}

export function setLanguage(language: string) {
  localStorage.setItem(LANGUAGE_KEY, language);
}

export function getVideoMode() {
  return localStorage.getItem(VIDEO_MODE_KEY);
}

export function setVideoMode(mode: string) {
  localStorage.setItem(VIDEO_MODE_KEY, mode);
}

export function getVideoScale(): number | null {
  const scale = localStorage.getItem(VIDEO_SCALE_KEY);
  if (scale && Number(scale)) {
    return Number(scale);
  }
  return null;
}

export function setVideoScale(scale: number): void {
  localStorage.setItem(VIDEO_SCALE_KEY, String(scale));
}

export function getMouseStyle() {
  return localStorage.getItem(MOUSE_STYLE_KEY);
}

export function setMouseStyle(mouse: string) {
  localStorage.setItem(MOUSE_STYLE_KEY, mouse);
}

export function getMouseMode() {
  return localStorage.getItem(MOUSE_MODE_KEY);
}

export function setMouseMode(mouse: string) {
  localStorage.setItem(MOUSE_MODE_KEY, mouse);
}

export function getInputAdapter() {
  return localStorage.getItem(INPUT_ADAPTER_KEY);
}

export function setInputAdapter(adapter: string) {
  localStorage.setItem(INPUT_ADAPTER_KEY, adapter);
}

export function getMouseScrollDirection(): number | null {
  const direction = localStorage.getItem(MOUSE_SCROLL_DIRECTION_KEY);
  if (direction && Number(direction)) {
    return Number(direction);
  }
  return null;
}

export function setMouseScrollDirection(direction: number): void {
  localStorage.setItem(MOUSE_SCROLL_DIRECTION_KEY, String(direction));
}

export function getMouseScrollInterval() {
  const interval = localStorage.getItem(MOUSE_SCROLL_INTERVAL_KEY);
  return interval ? Number(interval) : null;
}

export function setMouseScrollInterval(interval: number): void {
  localStorage.setItem(MOUSE_SCROLL_INTERVAL_KEY, String(interval));
}

export function setKeyboardSystem(system: string) {
  localStorage.setItem(KEYBOARD_SYSTEM_KEY, system);
}

export function getKeyboardSystem() {
  return localStorage.getItem(KEYBOARD_SYSTEM_KEY);
}

export function setKeyboardLanguage(language: string) {
  localStorage.setItem(KEYBOARD_LANGUAGE_KEY, language);
}

export function getKeyboardLanguage() {
  return localStorage.getItem(KEYBOARD_LANGUAGE_KEY);
}

export function setMenuDisabledItems(items: string[]) {
  const value = JSON.stringify(items);
  localStorage.setItem(MENU_DISABLED_ITEMS_KEY, value);
}

export function getMenuDisabledItems(): string[] {
  const value = localStorage.getItem(MENU_DISABLED_ITEMS_KEY);
  if (!value) return [];

  // A corrupt value must not break the menu; fall back to showing everything.
  try {
    const items: unknown = JSON.parse(value);
    return Array.isArray(items) ? items.filter((item) => typeof item === 'string') : [];
  } catch {
    return [];
  }
}

export function getMenuDisplayMode(): string {
  return localStorage.getItem(MENU_AUTO_HIDE_KEY) === 'always' ? 'always' : 'auto';
}

export function setMenuDisplayMode(mode: string) {
  localStorage.setItem(MENU_AUTO_HIDE_KEY, mode);
}

export function getTheme() {
  return localStorage.getItem(THEME_KEY);
}

export function setTheme(theme: string) {
  localStorage.setItem(THEME_KEY, theme);
}

export function getKeyboardLedStatusVisible(): boolean {
  const value = localStorage.getItem(KEYBOARD_LED_STATUS_VISIBLE_KEY);
  return value !== 'false';
}

export function setKeyboardLedStatusVisible(visible: boolean) {
  localStorage.setItem(KEYBOARD_LED_STATUS_VISIBLE_KEY, String(visible));
}

export function getHostLedStatusVisible(): boolean {
  return localStorage.getItem(HOST_LED_STATUS_VISIBLE_KEY) !== 'false';
}

export function setHostLedStatusVisible(visible: boolean) {
  localStorage.setItem(HOST_LED_STATUS_VISIBLE_KEY, String(visible));
}

// Lowest latency unless this browser chose smooth playback.
export function getDirectPlayback(): DirectPlayback {
  return localStorage.getItem('direct-playback') === 'paced' ? 'paced' : 'immediate';
}

export function setDirectPlayback(mode: DirectPlayback) {
  localStorage.setItem('direct-playback', mode);
}

// The layout of the host a paste is typed for, and how fast: the last ones
// chosen in this browser.
export function getPasteLayout(): string {
  return localStorage.getItem('nano-kvm-paste-layout') ?? 'en';
}

export function setPasteLayout(layout: string) {
  localStorage.setItem('nano-kvm-paste-layout', layout);
}

export function getPasteSpeed(): 'normal' | 'slow' {
  return localStorage.getItem('nano-kvm-paste-speed') === 'slow' ? 'slow' : 'normal';
}

export function setPasteSpeed(speed: 'normal' | 'slow') {
  localStorage.setItem('nano-kvm-paste-speed', speed);
}
