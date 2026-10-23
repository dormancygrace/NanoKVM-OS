import { Input } from 'antd';
import { useTranslation } from 'react-i18next';

import { defaultStorageIdentity, storageIdentityProblem } from '@/lib/usb-composition.ts';
import type { StorageIdentity } from '@/lib/usb-composition.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

export const StorageIdentitySection = ({
  identity,
  disabled,
  onChange
}: {
  identity: StorageIdentity;
  disabled: boolean;
  onChange: (identity: StorageIdentity) => void;
}) => {
  const { t } = useTranslation();
  return (
    <Panel className="space-y-4">
      <h3 className="m-0 font-medium">{t('settings.usb.storageIdentity.title')}</h3>
      <p className="text-fg-muted text-sm">{t('settings.usb.storageIdentity.description')}</p>
      {(Object.keys(defaultStorageIdentity) as (keyof StorageIdentity)[]).map((name) => (
        <SettingRow
          key={name}
          label={t(`settings.usb.storageIdentity.${name}`)}
          htmlFor={`usb-storage-${name}`}
        >
          <Input
            id={`usb-storage-${name}`}
            className="w-64 max-w-full"
            value={identity[name]}
            placeholder={
              defaultStorageIdentity[name] || t('settings.usb.storageIdentity.automatic')
            }
            maxLength={name === 'vendor' ? 8 : 16}
            allowClear
            disabled={disabled}
            onChange={(event) => onChange({ ...identity, [name]: event.target.value })}
          />
        </SettingRow>
      ))}
      {storageIdentityProblem(identity) && (
        <p className="text-warning text-sm">{t('settings.usb.storageIdentity.invalid')}</p>
      )}
    </Panel>
  );
};
