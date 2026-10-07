// VPN providers listed on the VPN page. A new provider is one entry here plus
// its settings page; the navigation does not change.
export const vpnProviders = [
  { id: 'wireguard', name: 'WireGuard', kind: 'profiles' },
  { id: 'openvpn', name: 'OpenVPN', kind: 'profiles' },
  { id: 'tailscale', name: 'Tailscale', kind: 'daemon' },
  { id: 'netbird', name: 'NetBird', kind: 'daemon' }
] as const;

export type VPNProviderId = (typeof vpnProviders)[number]['id'];

type Profile = { name: string; state: string; enabled?: boolean };

// The /api/extensions/<id>/status payloads: profile providers report
// availability and profiles, daemon providers one state.
export type VPNStatusData =
  { available: boolean; profiles: Profile[] } | { state: string; ip?: string; version?: string };

export type Tone = 'success' | 'warning' | 'danger' | 'neutral';

export type VPNSummary = {
  installed: boolean;
  // A dashboard.states key, or noProfiles.
  state: string;
  tone: Tone;
  // The enabled profile or the tunnel address.
  detail?: string;
};

const tones: Record<string, Tone> = {
  connected: 'success',
  running: 'success',
  connecting: 'warning',
  waiting: 'warning',
  idle: 'warning',
  notLogin: 'warning',
  auth: 'warning',
  error: 'danger'
};

export function summarizeVPN(data: VPNStatusData): VPNSummary {
  if ('profiles' in data) {
    if (!data.available) return { installed: false, state: 'notInstall', tone: 'neutral' };
    if (data.profiles.length === 0)
      return { installed: true, state: 'noProfiles', tone: 'neutral' };
    const enabled = data.profiles.find((profile) => profile.enabled);
    if (!enabled) return { installed: true, state: 'off', tone: 'neutral' };
    return {
      installed: true,
      state: enabled.state,
      tone: tones[enabled.state] ?? 'neutral',
      detail: enabled.name
    };
  }
  const installed = data.state !== 'notInstall';
  return {
    installed,
    state: data.state,
    tone: tones[data.state] ?? 'neutral',
    detail: data.state === 'running' && data.ip ? data.ip : undefined
  };
}
