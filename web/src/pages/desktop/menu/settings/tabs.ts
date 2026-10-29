// Settings navigation: groups, collections and the ids older links and
// requests still use.

// A tab id "<group>-<page>" is shown inside its expandable group.
export const settingsGroups = ['usb', 'network', 'services', 'system', 'ai', 'software'] as const;
export type SettingsGroup = (typeof settingsGroups)[number];

// The group's own entry, which expands it instead of opening a page.
export const isSettingsGroup = (id: string): id is SettingsGroup =>
  (settingsGroups as readonly string[]).includes(id);

export const settingsGroupOf = (id: string): SettingsGroup | undefined =>
  settingsGroups.find((group) => id.startsWith(`${group}-`));

// A collection is one navigation entry whose page lists its items; an item
// page "<collection>-<item>" opens from that list and is not in the menu, so
// adding VPN providers does not grow the navigation.
const settingsCollections = ['vpn'] as const;
type SettingsCollection = (typeof settingsCollections)[number];

export const settingsCollectionOf = (id: string): SettingsCollection | undefined =>
  settingsCollections.find((collection) => id.startsWith(`${collection}-`));

// The navigation entry that owns a page: its group or its collection.
export const settingsParentOf = (id: string) => settingsGroupOf(id) ?? settingsCollectionOf(id);

// Pages that moved: Device is part of System > General, Extensions became
// Software, and VPN providers were briefly inside Network.
const aliases: Record<string, string> = {
  // USB became a group: its composition and its identity.
  usb: 'usb-composition',
  device: 'system-general',
  'device-general': 'system-general',
  system: 'system-general',
  'system-software': 'software-packages',
  network: 'network-general',
  'date-time': 'system-date-time',
  tailscale: 'vpn-tailscale',
  'network-tailscale': 'vpn-tailscale',
  'network-netbird': 'vpn-netbird',
  'network-wireguard': 'vpn-wireguard',
  'network-openvpn': 'vpn-openvpn',
  // HTTPS, its certificate and SSH moved to System > Security.
  security: 'system-security',
  extensions: 'software-addons',
  // MCP moved from System to the AI group.
  'system-mcp': 'ai-mcp',
  ai: 'ai-screen-text',
  // Redfish/IPMI and RustDesk moved to Services.
  'system-bmc': 'services-bmc',
  'software-rustdesk': 'services-rustdesk',
  'extensions-rustdesk': 'services-rustdesk'
};

export const resolveSettingsTab = (id: string) => aliases[id] ?? id;
