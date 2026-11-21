import { Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import type { LldpStatus } from '@/api/extensions/netsvc.ts';
import { Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { ServicePage, type ServiceActions } from './service-page.tsx';

// Services > LLDP: the NanoKVM announces itself to the switch it is plugged
// into, and the page lists what the neighbours announce.
export const LldpPage = () => (
  <ServicePage<LldpStatus> name="lldp">
    {(status, actions) => <LldpSettings status={status} actions={actions} />}
  </ServicePage>
);

const LldpSettings = ({ status, actions }: { status: LldpStatus; actions: ServiceActions }) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.netServices.lldp' });
  return (
    <>
      <SettingsSection>
        <SettingRow label={t('cdp')} description={t('cdpHint')} htmlFor="service-lldp-cdp">
          <Switch
            id="service-lldp-cdp"
            checked={status.settings.cdp}
            loading={actions.saving}
            disabled={actions.busy}
            onChange={(cdp) => void actions.save({ cdp })}
          />
        </SettingRow>
      </SettingsSection>
      <SettingsSection title={t('neighbors')}>
        {status.neighbors.length === 0 ? (
          <p className="text-fg-muted m-0 text-sm">{t('noNeighbors')}</p>
        ) : (
          <Panel flush>
            <ul className="divide-line m-0 list-none divide-y p-0">
              {status.neighbors.map((neighbor, index) => (
                <li key={index} className="space-y-0.5 px-4 py-3">
                  <div className="text-fg flex flex-wrap items-baseline justify-between gap-x-3">
                    <span>{neighbor.name || neighbor.address || t('unnamed')}</span>
                    <span className="text-fg-muted text-xs">
                      {[neighbor.protocol, neighbor.interface].filter(Boolean).join(' · ')}
                    </span>
                  </div>
                  <div className="text-fg-muted text-xs">
                    {[
                      neighbor.port &&
                        t('port', {
                          port: neighbor.portDescription
                            ? `${neighbor.port} (${neighbor.portDescription})`
                            : neighbor.port
                        }),
                      neighbor.address
                    ]
                      .filter(Boolean)
                      .join(' · ')}
                  </div>
                  {neighbor.description && (
                    <div className="text-fg-muted text-xs break-words">{neighbor.description}</div>
                  )}
                </li>
              ))}
            </ul>
          </Panel>
        )}
      </SettingsSection>
    </>
  );
};
