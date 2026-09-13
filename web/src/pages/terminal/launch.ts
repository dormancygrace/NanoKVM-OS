const serialSessionKey = 'nanokvm.serial-session';

export function openSerialTerminal(query: string) {
  const tab = window.open('about:blank', '_blank');
  if (!tab) return;
  try {
    tab.sessionStorage.setItem(serialSessionKey, query);
    tab.opener = null;
    tab.location.replace('/#terminal');
  } catch {
    tab.close();
  }
}

export function readSerialSession(): URLSearchParams {
  // Accept old bookmarks once, then remove device settings from the address bar.
  const legacy = window.location.href.split('?')[1];
  if (legacy !== undefined) {
    window.sessionStorage.setItem(serialSessionKey, legacy);
    window.history.replaceState(null, '', '/#terminal');
  }
  return new URLSearchParams(window.sessionStorage.getItem(serialSessionKey) ?? '');
}
