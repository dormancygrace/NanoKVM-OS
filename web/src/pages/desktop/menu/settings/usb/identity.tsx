import { Alert, Input, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import {
  defaultUsbIdentity,
  identityProblem,
  matchingIdentityPreset,
  normalizeUsbId,
  usbIdentityPresets
} from '@/lib/usb-composition.ts';
import type { UsbIdentity, UsbStatus } from '@/lib/usb-composition.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

// The device descriptor the connected computer sees: a preset, or the
// fields by hand. Part of the composition draft; Apply writes it with the
// rest and reconnects USB.
export const UsbIdentitySection = ({
  status,
  identity,
  disabled,
  onChange
}: {
  status: UsbStatus;
  identity: UsbIdentity;
  disabled: boolean;
  onChange: (identity: UsbIdentity) => void;
}) => {
  const { t } = useTranslation();
  const preset = matchingIdentityPreset(identity);
  const problem = identityProblem(identity);
  const idsChanged =
    normalizeUsbId(identity.vendorId) !== defaultUsbIdentity.vendorId ||
    normalizeUsbId(identity.productId) !== defaultUsbIdentity.productId;

  function choose(id: string) {
    const chosen = usbIdentityPresets.find((item) => item.id === id);
    // a preset keeps the serial number
    if (chosen) onChange({ ...chosen.identity, serialNumber: identity.serialNumber });
  }

  const field = (name: Exclude<keyof UsbIdentity, 'serialNumber'>, mono = false) => (
    <SettingRow label={t(`settings.usb.identity.fields.${name}`)} htmlFor={`usb-identity-${name}`}>
      <Input
        id={`usb-identity-${name}`}
        className={mono ? 'w-32 font-mono' : 'w-64'}
        value={identity[name]}
        maxLength={mono ? 6 : 126}
        disabled={disabled}
        onChange={(event) => onChange({ ...identity, [name]: event.target.value })}
      />
    </SettingRow>
  );

  return (
    <div className="space-y-4">
      <SettingRow
        label={t('settings.usb.identity.title')}
        description={t('settings.usb.identity.description')}
        htmlFor="usb-identity-preset"
        stacked
      >
        <Select
          id="usb-identity-preset"
          className="w-full"
          value={preset}
          disabled={disabled}
          onChange={choose}
          options={[
            ...usbIdentityPresets.map((item) => ({
              value: item.id,
              label: t(`settings.usb.identity.presets.${item.id}`)
            })),
            { value: 'custom', label: t('settings.usb.identity.presets.custom') }
          ]}
        />
      </SettingRow>
      {field('vendorId', true)}
      {field('productId', true)}
      {field('manufacturer')}
      {field('product')}
      <SettingRow
        label={t('settings.usb.identity.fields.serialNumber')}
        description={t('settings.usb.identity.serialHint')}
        htmlFor="usb-identity-serialNumber"
      >
        <Input
          id="usb-identity-serialNumber"
          className="w-64 font-mono"
          value={identity.serialNumber ?? ''}
          placeholder={status.identity?.chipSerialNumber}
          maxLength={126}
          allowClear
          disabled={disabled}
          onChange={(event) => onChange({ ...identity, serialNumber: event.target.value })}
        />
      </SettingRow>
      {problem && (
        <div className="text-warning text-sm">{t(`settings.usb.identity.invalid.${problem}`)}</div>
      )}
      {idsChanged && !problem && (
        <Alert type="warning" showIcon message={t('settings.usb.identity.idWarning')} />
      )}
    </div>
  );
};
