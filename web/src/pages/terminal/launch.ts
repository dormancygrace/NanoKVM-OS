const serialSessionKey = 'nanokvm.serial-session';
// The shell this tab is attached to: a reload or a reconnect returns to it.
const shellSessionKey = 'nanokvm.shell-session';

function openTerminalTab(key: string, value: string) {
  const tab = window.open('about:blank', '_blank');
  if (!tab) return;
  try {
    // A new tab starts with a copy of this one's sessionStorage.
    tab.sessionStorage.removeItem(serialSessionKey);
    tab.sessionStorage.removeItem(shellSessionKey);
    tab.sessionStorage.setItem(key, value);
    tab.opener = null;
    tab.location.replace('/#terminal');
  } catch {
    tab.close();
  }
}

export function openSerialTerminal(query: string) {
  openTerminalTab(serialSessionKey, query);
}

// Opens a running shell in a new tab.
export function openShellTerminal(id: string) {
  openTerminalTab(shellSessionKey, id);
}

export function readSerialSession(): URLSearchParams {
  return new URLSearchParams(window.sessionStorage.getItem(serialSessionKey) ?? '');
}

export function readShellSession(): string {
  return window.sessionStorage.getItem(shellSessionKey) ?? '';
}

export function storeShellSession(id: string) {
  if (id) window.sessionStorage.setItem(shellSessionKey, id);
  else window.sessionStorage.removeItem(shellSessionKey);
}
