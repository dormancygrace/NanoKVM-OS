// The settings navigation as data, the way OpenWrt's LuCI menu.d and
// Cockpit manifests work: every entry names its place, built-in pages and
// pages added later (add-ons) go into the same list and are sorted
// together. Whether an entry shows (admin, hardware, installed add-on) is
// decided where the pages are rendered, not here.

import { settingsParentOf } from './tabs.ts';

export type SettingsMenuEntry = {
  // "<group>-<page>" inside a group; a group id is the group's own entry.
  id: string;
  // Position among its siblings. Built-ins use steps of 10 so that other
  // entries can go between them.
  order: number;
  // A name from the fixed icon set of the navigation.
  icon?: string;
  // Collection items: opened from their list, not shown in the menu.
  hidden?: boolean;
  // A translation key, or a fixed text such as a product name.
  title: { key: string } | { text: string };
};

export const builtinMenu = [
  { id: 'dashboard', order: 10, icon: 'dashboard', title: { key: 'dashboard.title' } },
  { id: 'video', order: 20, icon: 'video', title: { key: 'videoSettings.title' } },

  { id: 'usb', order: 30, icon: 'usb', title: { key: 'settings.usb.gadget' } },
  {
    id: 'usb-composition',
    order: 10,
    icon: 'layers',
    title: { key: 'settings.usb.compositionMenu' }
  },
  {
    id: 'usb-identity',
    order: 20,
    icon: 'fingerprint',
    title: { key: 'settings.usb.identityMenu' }
  },

  { id: 'network', order: 40, icon: 'network', title: { key: 'settings.network.title' } },
  {
    id: 'network-general',
    order: 10,
    icon: 'settings',
    title: { key: 'settings.network.general' }
  },
  { id: 'network-wifi', order: 20, icon: 'wifi', title: { key: 'settings.network.wifi.title' } },
  {
    id: 'network-ethernet',
    order: 30,
    icon: 'ethernet',
    title: { key: 'settings.network.ethernet.name' }
  },
  { id: 'network-bluetooth', order: 40, icon: 'bluetooth', title: { text: 'Bluetooth' } },
  { id: 'network-webrtc', order: 50, icon: 'radio-tower', title: { text: 'WebRTC' } },

  { id: 'vpn', order: 50, icon: 'vpn', title: { key: 'settings.vpn.title' } },
  { id: 'vpn-wireguard', order: 10, hidden: true, title: { text: 'WireGuard' } },
  { id: 'vpn-openvpn', order: 20, hidden: true, title: { text: 'OpenVPN' } },
  { id: 'vpn-tailscale', order: 30, hidden: true, title: { text: 'Tailscale' } },
  { id: 'vpn-netbird', order: 40, hidden: true, title: { text: 'NetBird' } },

  { id: 'services', order: 60, icon: 'server-cog', title: { key: 'settings.services.title' } },
  { id: 'services-bmc', order: 10, icon: 'server', title: { key: 'settings.bmc.title' } },
  { id: 'services-rustdesk', order: 20, icon: 'rustdesk', title: { text: 'RustDesk' } },

  { id: 'system', order: 70, icon: 'shield', title: { key: 'settings.system.title' } },
  { id: 'system-general', order: 10, icon: 'settings', title: { key: 'settings.system.device' } },
  { id: 'system-users', order: 20, icon: 'user', title: { key: 'settings.account.title' } },
  { id: 'system-security', order: 30, icon: 'lock', title: { key: 'settings.security.title' } },
  { id: 'system-date-time', order: 40, icon: 'clock', title: { key: 'dateTime.title' } },
  { id: 'system-recovery', order: 50, icon: 'download', title: { key: 'settings.recovery.title' } },
  { id: 'system-updates', order: 60, icon: 'download', title: { key: 'settings.updates.title' } },
  { id: 'system-memory', order: 70, icon: 'memory', title: { key: 'settings.memory.title' } },
  {
    id: 'system-diagnostics',
    order: 80,
    icon: 'stethoscope',
    title: { key: 'settings.system.diagnostics.title' }
  },

  { id: 'ai', order: 80, icon: 'sparkles', title: { key: 'settings.ai.title' } },
  {
    id: 'ai-screen-text',
    order: 10,
    icon: 'scan-text',
    title: { key: 'settings.ai.screenText.title' }
  },
  { id: 'ai-picoclaw', order: 20, icon: 'message', title: { text: 'PicoClaw' } },
  { id: 'ai-mcp', order: 30, icon: 'bot', title: { key: 'settings.mcp.title' } },

  { id: 'software', order: 90, icon: 'package', title: { key: 'settings.software.title' } },
  {
    id: 'software-addons',
    order: 10,
    icon: 'package',
    title: { key: 'settings.software.addons.title' }
  },
  {
    id: 'software-packages',
    order: 20,
    icon: 'package',
    title: { key: 'settings.software.addons.packages' }
  },

  { id: 'appearance', order: 100, icon: 'palette', title: { key: 'settings.appearance.title' } },
  { id: 'account', order: 110, icon: 'user', title: { key: 'settings.account.title' } },
  { id: 'about', order: 120, icon: 'info', title: { key: 'settings.about.title' } }
] as const satisfies readonly SettingsMenuEntry[];

// The ids of the built-in pages: the page table in index.tsx is keyed by
// them, so a misspelt id fails type checking instead of hiding a page.
export type BuiltinMenuId = (typeof builtinMenu)[number]['id'];

// The navigation order: top-level entries by their order, each group's and
// collection's entries right after it by theirs. An entry whose group or
// collection is not in the list is left out.
export function orderMenu(entries: readonly SettingsMenuEntry[]): SettingsMenuEntry[] {
  const byOrder = (a: SettingsMenuEntry, b: SettingsMenuEntry) =>
    a.order - b.order || a.id.localeCompare(b.id);
  const result: SettingsMenuEntry[] = [];
  for (const entry of entries.filter((item) => !settingsParentOf(item.id)).sort(byOrder)) {
    result.push(entry);
    result.push(
      ...entries.filter((child) => settingsParentOf(child.id) === entry.id).sort(byOrder)
    );
  }
  return result;
}
