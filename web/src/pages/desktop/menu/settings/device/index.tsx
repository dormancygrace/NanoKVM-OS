import { Divider } from 'antd';
import { useTranslation } from 'react-i18next';

import { Network } from '../network';
import { Hostname } from '../network/hostname';
import { Tls } from '../network/tls';
import { CPUFrequency } from './advanced/cpu-frequency';
import { Oled } from './oled.tsx';
import { Reboot } from './reboot.tsx';
import { Ssh } from './ssh.tsx';

export const Device = () => {
  const { t } = useTranslation();

  return (
    <>
      <div className="text-base">{t('settings.device.general')}</div>
      <Divider className="opacity-50" />

      <section aria-labelledby="general-device-heading" className="space-y-6">
        <h3 id="general-device-heading" className="text-sm font-medium text-neutral-400">
          {t('settings.device.sections.device')}
        </h3>
        <Hostname editable />
        <Oled />
        <CPUFrequency />
      </section>

      <Divider className="opacity-50" />

      <section aria-labelledby="general-network-heading" className="space-y-6">
        <h3 id="general-network-heading" className="text-sm font-medium text-neutral-400">
          {t('settings.device.sections.network')}
        </h3>
        <Network />
      </section>

      <Divider className="opacity-50" />

      <section aria-labelledby="general-access-heading" className="space-y-6">
        <h3 id="general-access-heading" className="text-sm font-medium text-neutral-400">
          {t('settings.device.sections.access')}
        </h3>
        <Tls />
        <Ssh />
      </section>

      <Divider className="opacity-50" />
      <Reboot />
    </>
  );
};
