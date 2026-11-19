import { Button, Segmented } from 'antd';
import { useTranslation } from 'react-i18next';

import type { UsbModeChoice, UsbPortPower } from '@/lib/usb-composition.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

type UsbModeProps = {
  // the mode in effect
  current: UsbModeChoice;
  selected: UsbModeChoice;
  // the selection needs the role changed on the device
  pending: boolean;
  busy: boolean;
  disabled: boolean;
  // what the port supplies in the host role, said before the switch to it
  portPower?: UsbPortPower;
  onSelect: (mode: UsbModeChoice) => void;
  onApply: () => void;
  onCancel: () => void;
};

export const UsbMode = ({
  current,
  selected,
  pending,
  busy,
  disabled,
  portPower,
  onSelect,
  onApply,
  onCancel
}: UsbModeProps) => {
  const { t } = useTranslation();
  const modes: UsbModeChoice[] = ['gadget', 'host', 'disabled'];

  return (
    <div className="space-y-3">
      <SettingRow
        label={t('settings.usb.role.label')}
        description={t(`settings.usb.role.${selected}.description`)}
        stacked
      >
        <Segmented
          block
          aria-label={t('settings.usb.role.label')}
          value={selected}
          disabled={disabled || busy}
          onChange={(value) => onSelect(value as UsbModeChoice)}
          options={modes.map((mode) => ({
            value: mode,
            label: t(`settings.usb.role.${mode}.title`)
          }))}
        />
      </SettingRow>
      {pending && (
        <>
          <p className="text-warning m-0 text-sm">
            {t(`settings.usb.role.${selected}.change`, {
              from: t(`settings.usb.role.${current}.title`)
            })}
          </p>
          {selected === 'host' && portPower && (
            <p className="text-fg-muted m-0 text-xs">{t(`settings.usb.host.power.${portPower}`)}</p>
          )}
          <div className="flex items-center gap-3">
            <Button type="primary" loading={busy} onClick={onApply}>
              {t('settings.usb.role.apply')}
            </Button>
            <Button disabled={busy} onClick={onCancel}>
              {t('settings.usb.cancel')}
            </Button>
          </div>
        </>
      )}
    </div>
  );
};
