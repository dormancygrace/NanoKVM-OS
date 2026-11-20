export const AUTH_EXPIRED_EVENT = 'nano-kvm:auth-expired';
export const PASSWORD_CHANGE_REQUIRED_EVENT = 'nano-kvm:password-change-required';
// The limit of inactivity was changed in this page.
export const IDLE_CHANGED_EVENT = 'nano-kvm:idle-changed';

export function notifyAuthExpired() {
  window.dispatchEvent(new Event(AUTH_EXPIRED_EVENT));
}

// The server refuses everything but the password change until the factory
// password is replaced; the route guard then opens the password page.
export function notifyPasswordChangeRequired() {
  window.dispatchEvent(new Event(PASSWORD_CHANGE_REQUIRED_EVENT));
}
