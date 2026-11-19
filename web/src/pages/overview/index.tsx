import { useEffect } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { adoptLiveCapture } from '@/lib/capture-control.ts';
import { subscribeLiveStatus } from '@/lib/live-status.ts';
import { atxEnabledAtom, atxLocalChange } from '@/jotai/atx.ts';
import { brandingAtom, brandingFavicon } from '@/jotai/branding';
import { settingsRequestAtom, webTitleAtom } from '@/jotai/settings.ts';
import { Head } from '@/components/head.tsx';
import { ThemeToggle } from '@/components/theme-toggle.tsx';
import { Logout } from '@/pages/desktop/menu/logout';
import { Power } from '@/pages/desktop/menu/power';
import { Settings } from '@/pages/desktop/menu/settings';
import { Dashboard } from '@/pages/desktop/menu/settings/dashboard';
import { Terminal } from '@/pages/desktop/menu/terminal';

import { ScreenPreview } from './screen-preview.tsx';

// The sections of the settings the bar leads to, each by its first page.
const sections = [
  { tab: 'video-general', title: 'videoSettings.title' },
  { tab: 'usb-composition', title: 'settings.usb.menu' },
  { tab: 'network-general', title: 'settings.network.title' },
  { tab: 'vpn', title: 'settings.vpn.title' },
  { tab: 'services-bmc', title: 'settings.services.title' },
  { tab: 'system-general', title: 'settings.system.title' },
  { tab: 'software-addons', title: 'settings.software.title' }
];

// The start page of a device that is managed more than it is watched: its
// state, with the remote screen one click away. It takes no part in the
// video: no stream, no input.
export const Overview = () => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const branding = useAtomValue(brandingAtom);
  const webTitle = useAtomValue(webTitleAtom);
  const openSettings = useSetAtom(settingsRequestAtom);
  const [atxEnabled, setAtxEnabled] = useAtom(atxEnabledAtom);

  // The capture and ATX states the panels and the bar show.
  useEffect(
    () =>
      subscribeLiveStatus((status, startedAt) => {
        adoptLiveCapture(status?.hdmi ?? null, startedAt);
        if (typeof status?.atx === 'boolean' && startedAt >= atxLocalChange.at) {
          setAtxEnabled(status.atx);
        }
      }),
    [setAtxEnabled]
  );

  return (
    <div className="bg-page text-fg h-dvh overflow-y-auto">
      <Head title={t('overview.title')} />
      <header className="border-line sticky top-0 z-10 border-b bg-neutral-900">
        <div className="mx-auto flex h-14 max-w-[1280px] items-center gap-3 px-4">
          <img src={brandingFavicon(branding)} alt="" className="size-8 shrink-0" />
          <span className="text-fg-strong truncate text-lg font-semibold">
            {webTitle || 'NanoKVM OS'}
          </span>
          {admin && (
            <nav
              aria-label={t('overview.sections')}
              className="ml-4 hidden min-w-0 items-center gap-1 overflow-x-auto md:flex"
            >
              {sections.map((section) => (
                <button
                  key={section.tab}
                  type="button"
                  className="nanokvm-button-base text-fg hover:text-fg-strong cursor-pointer rounded px-2.5 py-1.5 text-sm whitespace-nowrap hover:bg-neutral-700/70"
                  onClick={() => openSettings(section.tab)}
                >
                  {t(section.title)}
                </button>
              ))}
            </nav>
          )}
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {admin && <Terminal />}
            {atxEnabled && <Power />}
            <ThemeToggle />
            <Settings />
            <Logout />
          </div>
        </div>
      </header>
      <main className="mx-auto grid max-w-[1280px] gap-4 p-4 lg:grid-cols-[minmax(0,1fr)_360px]">
        <div className="min-w-0">
          <Dashboard navigate={openSettings} />
        </div>
        <aside className="order-first lg:order-none">
          <ScreenPreview />
        </aside>
      </main>
    </div>
  );
};
