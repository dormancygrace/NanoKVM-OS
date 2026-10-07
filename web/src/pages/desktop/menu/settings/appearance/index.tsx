import { useAuth } from '@/contexts/auth.ts';
import { useTranslation } from 'react-i18next';

import { SettingsSection } from '@/components/ui/settings.tsx';

import { BannerStyleSetting } from './banner-style.tsx';
import { Branding } from './branding.tsx';
import { ButtonColor } from './button-color.tsx';
import { KeyboardLedStatusSetting } from './keyboard-led-status.tsx';
import { Language } from './language.tsx';
import { MenuIcons } from './menu-icons.tsx';
import { MenuMode } from './menu-mode.tsx';
import { WebTitle } from './web-title.tsx';

export const Appearance = () => {
  const { t } = useTranslation();
  const { account } = useAuth();

  return (
    <div className="space-y-6">
      <SettingsSection title={t('settings.appearance.display')}>
        <Language />
      </SettingsSection>

      {account.role === 'admin' && (
        <SettingsSection title={t('settings.appearance.customize')}>
          <WebTitle />
          <Branding />
          <ButtonColor />
          <BannerStyleSetting />
        </SettingsSection>
      )}

      <SettingsSection title={t('settings.appearance.menuBar.title')}>
        <MenuMode />
        <KeyboardLedStatusSetting />
        <MenuIcons />
      </SettingsSection>
    </div>
  );
};
