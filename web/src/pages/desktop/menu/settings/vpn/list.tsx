import { useEffect, useState, type ReactNode } from 'react';
import { ChevronRightIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Netbird as NetbirdIcon } from '@/components/icons/netbird';
import { OpenVPNIcon } from '@/components/icons/openvpn';
import { Tailscale as TailscaleIcon } from '@/components/icons/tailscale';
import { WireGuardIcon } from '@/components/icons/wireguard';
import { Panel, StatusBadge } from '@/components/ui/settings.tsx';

import {
  summarizeVPN,
  vpnProviders,
  type VPNProviderId,
  type VPNStatusData,
  type VPNSummary
} from './providers.ts';

const icons: Record<VPNProviderId, ReactNode> = {
  wireguard: <WireGuardIcon />,
  openvpn: <OpenVPNIcon />,
  tailscale: <TailscaleIcon />,
  netbird: <NetbirdIcon />
};

// One row per provider; a row opens the provider page "vpn-<id>".
export const VPNList = ({ open }: { open: (tab: string) => void }) => {
  const { t } = useTranslation();
  const [summaries, setSummaries] = useState<Partial<Record<VPNProviderId, VPNSummary>>>({});
  const [versions, setVersions] = useState<Partial<Record<VPNProviderId, string>>>({});

  useEffect(() => {
    let active = true;
    const refresh = () => {
      for (const provider of vpnProviders) {
        void http
          .get(`/api/extensions/${provider.id}/status`)
          .then((rsp) => {
            if (!active || rsp.code !== 0) return;
            const data = rsp.data as VPNStatusData;
            setSummaries((current) => ({ ...current, [provider.id]: summarizeVPN(data) }));
            if ('version' in data && data.version) {
              setVersions((current) => ({ ...current, [provider.id]: data.version }));
            }
          })
          .catch(() => {});
      }
      void http
        .get('/api/extensions/vpn/versions')
        .then((rsp) => {
          if (active && rsp.code === 0) setVersions((current) => ({ ...current, ...rsp.data }));
        })
        .catch(() => {});
    };
    refresh();
    const stopPolling = pollWhileVisible(refresh, 5000);
    return () => {
      active = false;
      stopPolling();
    };
  }, []);

  // Installed providers first; the order within each part stays fixed.
  const providers = [...vpnProviders].sort(
    (a, b) =>
      Number(summaries[b.id]?.installed ?? false) - Number(summaries[a.id]?.installed ?? false)
  );

  return (
    <div className="space-y-4">
      <p className="text-fg-muted mt-0 text-sm">{t('settings.vpn.description')}</p>
      <Panel flush>
        <ul className="divide-line m-0 list-none divide-y p-0">
          {providers.map((provider) => {
            const summary = summaries[provider.id];
            const version = versions[provider.id];
            return (
              <li key={provider.id}>
                <button
                  type="button"
                  className="hover:bg-surface-raised flex w-full cursor-pointer items-center gap-3 border-0 bg-transparent px-4 py-3 text-left text-inherit"
                  onClick={() => open(`vpn-${provider.id}`)}
                >
                  <span className="flex size-5 shrink-0 items-center justify-center">
                    {icons[provider.id]}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate">{provider.name}</span>
                    {summary && (
                      <StatusBadge tone={summary.tone}>
                        {summary.state === 'noProfiles'
                          ? t('dashboard.noProfiles')
                          : t(`dashboard.states.${summary.state}`, {
                              defaultValue: summary.state
                            })}
                        {summary.detail ? ` · ${summary.detail}` : ''}
                      </StatusBadge>
                    )}
                  </span>
                  {version && summary?.installed && (
                    <span className="text-fg-muted shrink-0 text-xs">v{version}</span>
                  )}
                  <ChevronRightIcon size={16} className="text-fg-muted shrink-0" aria-hidden />
                </button>
              </li>
            );
          })}
        </ul>
      </Panel>
    </div>
  );
};
