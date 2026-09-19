import type { WifiBand, WifiNetwork } from '@/api/network.ts';

export type WifiGroup = WifiNetwork & { bands: WifiBand[]; candidates: WifiNetwork[] };

// Combine modern Personal modes, retaining the exact security for each candidate.
// Open, legacy WPA and unsupported authentication remain separate.
export function groupWifiNetworks(networks: WifiNetwork[]): WifiGroup[] {
  const groups = new Map<string, WifiGroup>();
  for (const network of networks) {
    const securityGroup = ['wpa2', 'wpa3', 'wpa2-wpa3'].includes(network.security)
      ? 'modern-personal'
      : network.security;
    const key = JSON.stringify([network.ssid, securityGroup]);
    const previous = groups.get(key);
    const bands = Array.from(new Set([...(previous?.bands || []), network.band])).sort();
    groups.set(key, {
      ...(!previous || network.signal > previous.signal ? network : previous),
      bands,
      candidates: [...(previous?.candidates || []), network]
    });
  }
  return [...groups.values()].sort((a, b) => b.signal - a.signal);
}
