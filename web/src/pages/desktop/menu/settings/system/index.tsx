import { Divider } from 'antd';
import { useTranslation } from 'react-i18next';

import { CPUFrequency } from '../device/advanced/cpu-frequency.tsx';
import { Reboot } from '../device/reboot.tsx';
import { Ssh } from '../device/ssh.tsx';

export const System = () => {
  const { t } = useTranslation();

  return (
    <>
      <div className="text-base">{t('settings.system.title')}</div>
      <Divider className="opacity-50" />
      <section aria-labelledby="system-services-heading" className="space-y-6">
        <h3 id="system-services-heading" className="text-sm font-medium text-neutral-400">
          {t('settings.system.services')}
        </h3>
        <Ssh />
      </section>
      <Divider className="opacity-50" />
      <section aria-labelledby="system-performance-heading" className="space-y-6">
        <h3 id="system-performance-heading" className="text-sm font-medium text-neutral-400">
          {t('settings.system.performance')}
        </h3>
        <CPUFrequency />
      </section>
      <Divider className="opacity-50" />
      <Reboot />
    </>
  );
};
