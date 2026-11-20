import { useEffect, useState } from 'react';
import { Alert, Button, Popconfirm, Radio, Segmented, Select, Switch, Typography } from 'antd';
import { useTranslation } from 'react-i18next';

import type { UpsSettingsRequest, UpsStatus } from '@/api/extensions/netsvc.ts';
import { getUsbHostDevices, type UsbHostDevice } from '@/api/virtual-device.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Fold, Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

import { ServicePage, type ServiceActions } from './service-page.tsx';

// Services > UPS: Network UPS Tools reads a UPS attached to the NanoKVM and
// shares its state with the computers the UPS feeds.
export const UpsPage = () => (
  <ServicePage<UpsStatus> name="ups">
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

const usbPort = 'auto';
// The serial ports on the boards' headers.
const serialPorts = ['/dev/ttyS1', '/dev/ttyS2'];

// NUT names its USB drivers so; nutdrv_qx speaks to both kinds of port.
const isUsbDriver = (name: string) => name.includes('usb') || name === 'nutdrv_qx';
// Drivers that read no port of the NanoKVM: other servers and simulations.
const noPortDrivers = [
  'apcupsd-ups',
  'clone',
  'clone-outlet',
  'dummy-ups',
  'netxml-ups',
  'snmp-ups'
];
const isSerialDriver = (name: string) => !name.includes('usb') && !noPortDrivers.includes(name);

type Draft = { driver: string; port: string; usbDevice: string };

const usbId = (device: UsbHostDevice) => `${device.vendorId}:${device.productId}`.toLowerCase();

const UpsForm = ({ status, actions }: { status: UpsStatus; actions: ServiceActions }) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.netServices.ups' });
  const saved = status.settings;
  // What is being chosen; without it the form shows what the device has.
  const [edited, setEdited] = useState<Draft>();
  const draft = edited ?? { driver: saved.driver, port: saved.port, usbDevice: saved.usbDevice };
  const usb = draft.port === usbPort;
  const [attached, setAttached] = useState<UsbHostDevice[]>([]);

  // What is attached to the USB port, while that is what the choice is about.
  const listUsb = usb && status.usbHost;
  useEffect(() => {
    if (!listUsb) return;
    let active = true;
    const load = () =>
      getUsbHostDevices()
        .then((rsp) => {
          if (active && rsp.code === 0) setAttached(rsp.data.devices);
        })
        .catch(() => {
          /* the last list stays */
        });
    void load();
    const stop = pollWhileVisible(() => void load(), 3000);
    return () => {
      active = false;
      stop();
    };
  }, [listUsb]);

  const setKind = (kind: string) => {
    if ((kind === 'usb') === usb) return;
    setEdited(
      kind === 'usb'
        ? {
            driver: isUsbDriver(draft.driver) ? draft.driver : 'usbhid-ups',
            port: usbPort,
            usbDevice: ''
          }
        : {
            driver: isSerialDriver(draft.driver) ? draft.driver : 'nutdrv_qx',
            port: serialPorts[0],
            usbDevice: ''
          }
    );
  };

  const request = (change: Partial<UpsSettingsRequest>): UpsSettingsRequest => ({
    driver: saved.driver,
    port: saved.port,
    usbDevice: saved.usbDevice,
    network: saved.network,
    ...change
  });
  const changed =
    draft.driver !== saved.driver ||
    draft.port !== saved.port ||
    draft.usbDevice !== saved.usbDevice;
  const drivers = status.drivers.filter(usb ? isUsbDriver : isSerialDriver);
  // One entry per kind of device; the chosen one stays listed when unplugged.
  const usbChoices = new Map<string, string>();
  if (listUsb) {
    for (const device of attached) {
      const name = [device.manufacturer, device.product].filter(Boolean).join(' ');
      usbChoices.set(usbId(device), name ? `${name} (${usbId(device)})` : usbId(device));
    }
  }
  if (draft.usbDevice && !usbChoices.has(draft.usbDevice)) {
    usbChoices.set(draft.usbDevice, t('notAttached', { id: draft.usbDevice }));
  }
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
        <SettingRow label={t('connection')} stacked>
          <Segmented
            block
            aria-label={t('connection')}
            value={usb ? 'usb' : 'serial'}
            disabled={actions.busy}
            onChange={setKind}
            options={[
              { value: 'usb', label: t('usb') },
              { value: 'serial', label: t('serial') }
            ]}
          />
        </SettingRow>
        {usb ? (
          <SettingRow
            stacked
            label={t('usbDevice')}
            description={status.usbHost && attached.length === 0 ? t('noUsbDevices') : undefined}
          >
            <Radio.Group
              aria-label={t('usbDevice')}
              className="flex flex-col gap-2"
              value={draft.usbDevice}
              disabled={actions.busy}
              onChange={(e) => setEdited({ ...draft, usbDevice: e.target.value })}
            >
              <Radio value="">{t('anyUsb')}</Radio>
              {[...usbChoices].map(([id, label]) => (
                <Radio key={id} value={id}>
                  {label}
                </Radio>
              ))}
            </Radio.Group>
          </SettingRow>
        ) : (
          <SettingRow stacked label={t('serialPort')} description={t('serialHint')}>
            <Radio.Group
              aria-label={t('serialPort')}
              value={draft.port}
              disabled={actions.busy}
              onChange={(e) => setEdited({ ...draft, port: e.target.value })}
            >
              {serialPorts.map((port) => (
                <Radio key={port} value={port}>
                  <code>{port}</code>
                </Radio>
              ))}
            </Radio.Group>
          </SettingRow>
        )}
        <SettingRow
          stacked
          label={t('driver')}
          description={usb ? t('driverHint') : t('driverHintSerial')}
          htmlFor="service-ups-driver"
        >
          <Select
            id="service-ups-driver"
            className="w-full"
            showSearch
            value={draft.driver}
            disabled={actions.busy}
            options={drivers.map((name) => ({ value: name, label: name }))}
            onChange={(driver) => setEdited({ ...draft, driver })}
          />
        </SettingRow>
        <div className="flex justify-end">
          <Button
            type="primary"
            loading={actions.saving}
            disabled={actions.busy || !changed}
            onClick={async () => {
              if (await actions.save(request(draft))) setEdited(undefined);
            }}
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
