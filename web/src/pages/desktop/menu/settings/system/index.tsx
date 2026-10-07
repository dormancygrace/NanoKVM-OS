import { useTranslation } from 'react-i18next';

import { SettingsSection } from '@/components/ui/settings.tsx';

import { CPUFrequency } from '../device/advanced/cpu-frequency.tsx';
import { Oled } from '../device/oled.tsx';
import { Reboot } from '../device/reboot.tsx';
import { Ssh } from '../device/ssh.tsx';

export const System = () => {
  const { t } = useTranslation();

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.system.device')}>
        <Oled />
      </SettingsSection>
      <SettingsSection title={t('settings.system.services')}>
        <Ssh />
      </SettingsSection>
      <SettingsSection title={t('settings.system.performance')}>
        <CPUFrequency />
      </SettingsSection>
      <Reboot />
    </div>
  );
};
