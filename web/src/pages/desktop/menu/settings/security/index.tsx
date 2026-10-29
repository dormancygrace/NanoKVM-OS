import { SettingsSection } from '@/components/ui/settings.tsx';

import { Ssh } from '../device/ssh.tsx';
import { Tls } from '../network/tls.tsx';

// Access to the device: SSH first, then HTTPS with its certificate.
export const Security = () => (
  <div className="space-y-6">
    <SettingsSection>
      <Ssh />
    </SettingsSection>
    <SettingsSection>
      <Tls />
    </SettingsSection>
  </div>
);
