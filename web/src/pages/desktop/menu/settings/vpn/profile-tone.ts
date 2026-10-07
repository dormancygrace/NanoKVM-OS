// StatusBadge tone for a VPN profile state (WireGuard and OpenVPN).
export const profileTone = (state: string) =>
  state === 'connected' ? 'success' : state === 'error' ? 'danger' : 'neutral';
