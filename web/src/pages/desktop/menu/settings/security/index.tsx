import { SettingsSection } from '@/components/ui/settings.tsx';

import { BannerStyleSetting } from '../device/ssh-banner.tsx';
import { Ssh } from '../device/ssh.tsx';
import { Tls } from '../network/tls.tsx';

// Access to the device: SSH (with the banner of its sessions) first, then
// HTTPS with its certificate.
export const Security = () => (
  <div className="space-y-6">
    <SettingsSection>
      <Ssh />
      <BannerStyleSetting />
    </SettingsSection>
    <SettingsSection>
      <Tls />
    </SettingsSection>
  </div>
);
