import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useParams } from 'react-router-dom';

import { Head } from '@/components/head.tsx';
import { Settings, type SettingsPageHost } from '@/pages/desktop/menu/settings';

import { settingsPath } from './paths.ts';
import { OverviewShell } from './shell.tsx';

// The settings as pages under the bar: the navigation and the pages of the
// settings dialog, with the open one in the address.
export const SettingsPage = () => {
  const { t } = useTranslation();
  const { tab } = useParams();
  const navigate = useNavigate();
  const page = useMemo<SettingsPageHost>(
    () => ({
      tab,
      onTab: (next) => navigate(settingsPath(next)),
      onClose: () => navigate('/overview'),
      onOpenScreen: () => navigate('/screen')
    }),
    [tab, navigate]
  );

  return (
    <OverviewShell tab={tab ?? ''}>
      <Head title={t('settings.title')} />
      <Settings page={page} />
    </OverviewShell>
  );
};
