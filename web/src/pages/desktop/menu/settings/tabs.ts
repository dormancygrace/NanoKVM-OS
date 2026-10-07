// Settings navigation: groups and the ids older links and requests still use.

// A tab id "<group>-<page>" is shown inside its group.
export const settingsGroups = ['network', 'system', 'software'] as const;
export type SettingsGroup = (typeof settingsGroups)[number];

export const settingsGroupOf = (id: string): SettingsGroup | undefined =>
  settingsGroups.find((group) => id.startsWith(`${group}-`));

// Pages that moved: Device is part of System > General, VPN providers are in
// Network, Extensions became Software.
const aliases: Record<string, string> = {
  device: 'system-general',
  'device-general': 'system-general',
  system: 'system-general',
  'system-software': 'software-packages',
  network: 'network-general',
  'date-time': 'system-date-time',
  vpn: 'network-tailscale',
  tailscale: 'network-tailscale',
  'vpn-tailscale': 'network-tailscale',
  'vpn-netbird': 'network-netbird',
  'vpn-wireguard': 'network-wireguard',
  'vpn-openvpn': 'network-openvpn',
  extensions: 'software-addons',
  'extensions-rustdesk': 'software-rustdesk'
};

export const resolveSettingsTab = (id: string) => aliases[id] ?? id;
