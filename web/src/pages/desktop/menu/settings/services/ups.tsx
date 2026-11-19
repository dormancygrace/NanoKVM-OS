import { useState } from 'react';
import { Alert, Button, Input, Popconfirm, Select, Switch, Typography } from 'antd';
import { useTranslation } from 'react-i18next';

import type { UpsSettingsRequest, UpsStatus } from '@/api/extensions/netsvc.ts';
import { Fold, Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { ServicePage, type ServiceActions } from './service-page.tsx';

type PageProps = { setIsLocked: (isLocked: boolean) => void };

// Services > UPS: Network UPS Tools reads a UPS attached to the NanoKVM and
// shares its state with the computers the UPS feeds.
export const UpsPage = ({ setIsLocked }: PageProps) => (
  <ServicePage<UpsStatus> name="ups" setIsLocked={setIsLocked}>
    {(status, actions) => <UpsForm status={status} actions={actions} />}
  </ServicePage>
);

// The readings most people look for, in this order; the rest is folded.
const mainValues = [
  'device.model',
  'ups.status',
  'battery.charge',
  'battery.runtime',
  'input.voltage',
  'output.voltage',
  'ups.load'
];

const portRule = /^[A-Za-z0-9_./:@-]{1,64}$/;

const UpsForm = ({ status, actions }: { status: UpsStatus; actions: ServiceActions }) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.netServices.ups' });
  const saved = status.settings;
  const [driver, setDriver] = useState(saved.driver);
  const [port, setPort] = useState(saved.port);

  const request = (change: Partial<UpsSettingsRequest>): UpsSettingsRequest => ({
    driver: saved.driver,
    port: saved.port,
    network: saved.network,
    ...change
  });
  const portValid = portRule.test(port.trim());
  const changed = driver !== saved.driver || port.trim() !== saved.port;
  const usb = saved.port === 'auto';
  const names = Object.keys(status.values).sort();
  const main = mainValues.filter((name) => name in status.values);
  const rest = names.filter((name) => !mainValues.includes(name));
  const address = `ups@${window.location.hostname}`;

  const values = (list: string[]) => (
    <dl className="m-0 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      {list.map((name) => (
        <div key={name} className="contents">
          <dt className="text-fg-muted">{name}</dt>
          <dd className="text-fg m-0 break-words">{status.values[name]}</dd>
        </div>
      ))}
    </dl>
  );

  return (
    <>
      {usb && !status.usbHost && <Alert type="warning" showIcon message={t('usbNotHost')} />}
      {!status.running && <Alert type="info" showIcon message={t('notRunningHint')} />}
      <SettingsSection title={t('device')}>
        <SettingRow
          stacked
          label={t('driver')}
          description={t('driverHint')}
          htmlFor="service-ups-driver"
        >
          <Select
            id="service-ups-driver"
            className="w-full"
            showSearch
            value={driver}
            options={status.drivers.map((name) => ({ value: name, label: name }))}
            onChange={setDriver}
          />
        </SettingRow>
        <SettingRow
          stacked
          label={t('port')}
          description={t('portHint')}
          htmlFor="service-ups-port"
        >
          <Input
            id="service-ups-port"
            value={port}
            maxLength={64}
            status={portValid ? undefined : 'error'}
            spellCheck={false}
            onChange={(e) => setPort(e.target.value)}
          />
        </SettingRow>
        <div className="flex justify-end">
          <Button
            type="primary"
            loading={actions.saving}
            disabled={actions.busy || !changed || !portValid}
            onClick={() => void actions.save(request({ driver, port: port.trim() }))}
          >
            {t('save')}
          </Button>
        </div>
      </SettingsSection>
      <SettingsSection title={t('sharing')}>
        <SettingRow label={t('network')} description={t('networkHint')} htmlFor="service-ups-net">
          <Switch
            id="service-ups-net"
            checked={saved.network}
            disabled={actions.busy}
            onChange={(network) => void actions.save(request({ network }))}
          />
        </SettingRow>
        {saved.network && (
          <Panel>
            <dl className="m-0 grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-1 text-sm">
              <dt className="text-fg-muted">{t('address')}</dt>
              <dd className="m-0">
                <Typography.Text copyable>{address}</Typography.Text>
              </dd>
              <dt className="text-fg-muted">{t('user')}</dt>
              <dd className="m-0">
                <Typography.Text copyable>{saved.user}</Typography.Text>
              </dd>
              <dt className="text-fg-muted">{t('password')}</dt>
              <dd className="m-0">
                <Typography.Text copyable>{saved.password}</Typography.Text>
              </dd>
            </dl>
            <div className="mt-3 flex justify-end">
              <Popconfirm
                title={t('newPasswordConfirm')}
                okText={t('newPassword')}
                onConfirm={() => void actions.save(request({ newPassword: true }))}
              >
                <Button disabled={actions.busy}>{t('newPassword')}</Button>
              </Popconfirm>
            </div>
          </Panel>
        )}
      </SettingsSection>
      {status.running && (
        <SettingsSection title={t('state')}>
          {names.length === 0 ? (
            <p className="text-fg-muted m-0 text-sm">{t('noValues')}</p>
          ) : (
            <>
              {main.length > 0 && <Panel>{values(main)}</Panel>}
              {rest.length > 0 && <Fold title={t('allValues')}>{values(rest)}</Fold>}
            </>
          )}
        </SettingsSection>
      )}
    </>
  );
};
