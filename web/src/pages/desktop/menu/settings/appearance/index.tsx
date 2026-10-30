import { useAuth } from '@/contexts/auth.ts';
import { useTranslation } from 'react-i18next';

import { SettingsSection } from '@/components/ui/settings.tsx';

import { Branding } from './branding.tsx';
import { ButtonColor } from './button-color.tsx';
import { KeyboardLedStatusSetting } from './keyboard-led-status.tsx';
import { Language } from './language.tsx';
import { MenuIcons } from './menu-icons.tsx';
import { MenuMode } from './menu-mode.tsx';
import { WebTitle } from './web-title.tsx';

// Two kinds of settings, by where they are kept: this browser only (local
// storage), or on the device for everyone who opens it.
export const Appearance = () => {
  const { t } = useTranslation();
  const { account } = useAuth();

  return (
    <div className="space-y-6">
      <SettingsSection
        title={t('settings.appearance.browser')}
        description={t('settings.appearance.browserDesc')}
      >
        <Language />
        <MenuMode />
        <KeyboardLedStatusSetting />
        <MenuIcons />
      </SettingsSection>

      {account.role === 'admin' && (
        <SettingsSection
          title={t('settings.appearance.device')}
          description={t('settings.appearance.deviceDesc')}
        >
          <WebTitle />
          <ButtonColor />
          <Branding />
        </SettingsSection>
      )}
    </div>
  );
};
