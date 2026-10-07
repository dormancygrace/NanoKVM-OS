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
const MOBILE_MENU_PLACEMENT_KEY = 'nano-kvm-mobile-menu-placement';
const KEYBOARD_LED_STATUS_VISIBLE_KEY = 'nano-kvm-keyboard-led-status-visible';
const POWER_CONFIRM_KEY = 'nano-kvm-power-confirm';

// Keys older versions wrote but nothing reads any more.
const LEGACY_KEYS = [
  'nano-kvm-web-resolution',
  'nano-kvm-fps',
  'nano-kvm-quality',
  'nano-kvm-gop',
  'nano-kvm-skip-modify-password',
  'nano-kvm-frame-detect'
];

export function removeLegacyKeys() {
  try {
    for (const key of LEGACY_KEYS) localStorage.removeItem(key);
  } catch {
    // Storage may be unavailable (blocked site data); nothing to clean up.
  }
}

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
  return value ? JSON.parse(value) : [];
}

export function getMenuDisplayMode(): string {
  const value = localStorage.getItem(MENU_AUTO_HIDE_KEY);
  return value || 'auto';
}

export function setMenuDisplayMode(mode: string) {
  localStorage.setItem(MENU_AUTO_HIDE_KEY, mode);
}

export function getMobileMenuPlacement(): unknown {
  const value = localStorage.getItem(MOBILE_MENU_PLACEMENT_KEY);
  if (!value) return null;

  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}

export function setMobileMenuPlacement(placement: { edge: string; top: number }) {
  localStorage.setItem(MOBILE_MENU_PLACEMENT_KEY, JSON.stringify(placement));
}

export function getKeyboardLedStatusVisible(): boolean {
  const value = localStorage.getItem(KEYBOARD_LED_STATUS_VISIBLE_KEY);
  return value !== 'false';
}

export function setKeyboardLedStatusVisible(visible: boolean) {
  localStorage.setItem(KEYBOARD_LED_STATUS_VISIBLE_KEY, String(visible));
}

export function getPowerConfirm() {
  const enabled = localStorage.getItem(POWER_CONFIRM_KEY);
  return enabled !== 'false';
}

export function setPowerConfirm(enabled: boolean) {
  localStorage.setItem(POWER_CONFIRM_KEY, String(enabled));
}


export type DirectPlayback = 'paced' | 'immediate';

export function getDirectPlayback(): DirectPlayback {
  return localStorage.getItem('direct-playback') === 'immediate' ? 'immediate' : 'paced';
}

export function setDirectPlayback(mode: DirectPlayback) {
  localStorage.setItem('direct-playback', mode);
}
