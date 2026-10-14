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
  return new URLSearchParams(window.sessionStorage.getItem(serialSessionKey) ?? '');
}
