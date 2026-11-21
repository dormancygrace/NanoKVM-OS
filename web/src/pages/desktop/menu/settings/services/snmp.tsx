import { useState } from 'react';
import { Button, Input } from 'antd';
import { useTranslation } from 'react-i18next';

import type { SnmpSettings, SnmpStatus } from '@/api/extensions/netsvc.ts';
import { SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { ServicePage, type ServiceActions } from './service-page.tsx';

// Services > SNMP: a read-only SNMP v2c agent for a monitoring system.
export const SnmpPage = () => (
  <ServicePage<SnmpStatus> name="snmp">
    {(status, actions) => <SnmpForm status={status} actions={actions} />}
  </ServicePage>
);

// The same rules as the server's.
const communityRule = /^[A-Za-z0-9_.-]{8,32}$/;
const networkRule = /^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$/;

const SnmpForm = ({ status, actions }: { status: SnmpStatus; actions: ServiceActions }) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.netServices.snmp' });
  const saved = status.settings;
  // What is being typed; without it the form shows what the device has. The
  // device may save the source in another form (192.168.1.7/24 as
  // 192.168.1.0/24), so a saved form shows the device's again.
  const [edited, setEdited] = useState<SnmpSettings>();
  const draft = edited ?? saved;
  const set = (change: Partial<SnmpSettings>) => setEdited({ ...draft, ...change });

  const network = draft.network.trim();
  const communityValid = communityRule.test(draft.community);
  const networkValid = network === '' || networkRule.test(network);
  const changed =
    draft.community !== saved.community ||
    network !== saved.network ||
    draft.location.trim() !== saved.location ||
    draft.contact.trim() !== saved.contact;

  return (
    <SettingsSection title={t('settings')} description={t('settingsHint')}>
      <SettingRow
        stacked
        label={t('community')}
        description={t('communityHint')}
        htmlFor="service-snmp-community"
      >
        <Input
          id="service-snmp-community"
          value={draft.community}
          maxLength={32}
          status={communityValid ? undefined : 'error'}
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => set({ community: e.target.value })}
        />
      </SettingRow>
      <SettingRow
        stacked
        label={t('network')}
        description={t('networkHint')}
        htmlFor="service-snmp-network"
      >
        <Input
          id="service-snmp-network"
          value={draft.network}
          maxLength={64}
          placeholder="192.168.1.0/24"
          status={networkValid ? undefined : 'error'}
          onChange={(e) => set({ network: e.target.value })}
        />
      </SettingRow>
      <SettingRow stacked label={t('location')} htmlFor="service-snmp-location">
        <Input
          id="service-snmp-location"
          value={draft.location}
          maxLength={64}
          onChange={(e) => set({ location: e.target.value })}
        />
      </SettingRow>
      <SettingRow stacked label={t('contact')} htmlFor="service-snmp-contact">
        <Input
          id="service-snmp-contact"
          value={draft.contact}
          maxLength={64}
          onChange={(e) => set({ contact: e.target.value })}
        />
      </SettingRow>
      <div className="flex justify-end">
        <Button
          type="primary"
          loading={actions.saving}
          disabled={actions.busy || !changed || !communityValid || !networkValid}
          onClick={async () => {
            if (await actions.save({ ...draft, network })) setEdited(undefined);
          }}
        >
          {t('save')}
        </Button>
      </div>
    </SettingsSection>
  );
};
