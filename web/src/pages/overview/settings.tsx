import { useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useBlocker, useNavigate, useParams } from 'react-router-dom';

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
  // A page at work locks the navigation of the settings. Leaving it by the
  // bar, by a link from elsewhere or by the browser's Back would end what it
  // is doing (an installation whose progress it follows) just the same.
  const [locked, setLocked] = useState(false);
  const blocker = useBlocker(locked);
  useEffect(() => {
    if (blocker.state === 'blocked') blocker.reset();
  }, [blocker]);

  const page = useMemo<SettingsPageHost>(
    () => ({
      tab,
      onTab: (next) => navigate(settingsPath(next)),
      onClose: () => navigate('/overview'),
      onOpenScreen: () => navigate('/screen'),
      onLock: setLocked
    }),
    [tab, navigate]
  );

  return (
    <OverviewShell tab={tab ?? ''} locked={locked}>
      <Head title={t('settings.title')} />
      <Settings page={page} />
    </OverviewShell>
  );
};
