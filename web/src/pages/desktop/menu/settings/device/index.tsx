import { Divider } from 'antd';
import { useTranslation } from 'react-i18next';

import { Oled } from './oled.tsx';
import { SystemInformation } from './system-information.tsx';

export const Device = () => {
  const { t } = useTranslation();

  return (
    <>
      <div className="text-base">{t('settings.device.general')}</div>
      <Divider className="opacity-50" />

      <section className="space-y-6">
        <Oled />
        <SystemInformation />
      </section>
    </>
  );
};
