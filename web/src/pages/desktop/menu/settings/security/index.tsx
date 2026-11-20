import { useTranslation } from 'react-i18next';

import { SettingsSection } from '@/components/ui/settings.tsx';

import { BannerStyleSetting } from '../device/ssh-banner.tsx';
import { Ssh } from '../device/ssh.tsx';
import { Tls } from '../network/tls.tsx';
import { IdleLimit } from './idle.tsx';

// Access to the device: SSH (with the banner of its sessions) first, then
// HTTPS with its certificate, the sign-out after inactivity, then a pointer
// to the watermark.
export const Security = ({ onOpenWatermark }: { onOpenWatermark: () => void }) => {
  const { t } = useTranslation();
  return (
    <div className="space-y-6">
      <SettingsSection>
        <Ssh />
        <BannerStyleSetting />
      </SettingsSection>
      <SettingsSection>
        <Tls />
      </SettingsSection>
      <IdleLimit />
      <p className="text-fg-muted m-0 text-xs">
        {t('settings.security.watermark')}{' '}
        <button
          type="button"
          className="nanokvm-button-base text-info cursor-pointer underline"
          onClick={onOpenWatermark}
        >
          {t('settings.security.watermarkLink')}
        </button>
      </p>
    </div>
  );
};
