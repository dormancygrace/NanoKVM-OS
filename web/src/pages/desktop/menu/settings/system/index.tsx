import { SettingsSection } from '@/components/ui/settings.tsx';

import { CPUFrequency } from '../device/advanced/cpu-frequency.tsx';
import { Atx } from '../device/atx.tsx';
import { Oled } from '../device/oled.tsx';

export const System = () => (
  <div className="space-y-6">
    <SettingsSection>
      <Oled />
    </SettingsSection>
    <SettingsSection>
      <Atx />
    </SettingsSection>
    <CPUFrequency />
  </div>
);
