import { useEffect, type MouseEvent, type ReactNode } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import clsx from 'clsx';
import { useAtom, useAtomValue } from 'jotai';
import { SettingsIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link, useNavigate } from 'react-router-dom';

import { adoptLiveCapture } from '@/lib/capture-control.ts';
import { subscribeLiveStatus } from '@/lib/live-status.ts';
import { atxEnabledAtom, atxLocalChange } from '@/jotai/atx.ts';
import { brandingAtom, brandingFavicon } from '@/jotai/branding';
import { settingsRequestAtom, webTitleAtom } from '@/jotai/settings.ts';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';
import { ThemeToggle } from '@/components/theme-toggle.tsx';
import { Capture } from '@/pages/desktop/menu/capture.tsx';
import { Logout } from '@/pages/desktop/menu/logout';
import { Power } from '@/pages/desktop/menu/power';
import { resolveSettingsTab, settingsParentOf } from '@/pages/desktop/menu/settings/tabs.ts';
import { Terminal } from '@/pages/desktop/menu/terminal';

import { settingsPath } from './paths.ts';

// The sections of the settings the bar leads to, each by its first page.
const sections = [
  { id: 'video', tab: 'video-general', title: 'videoSettings.title' },
  { id: 'usb', tab: 'usb-composition', title: 'settings.usb.menu' },
  { id: 'network', tab: 'network-general', title: 'settings.network.title' },
  { id: 'vpn', tab: 'vpn', title: 'settings.vpn.title' },
  { id: 'services', tab: 'services-bmc', title: 'settings.services.title' },
  { id: 'system', tab: 'system-general', title: 'settings.system.title' },
  { id: 'software', tab: 'software-addons', title: 'settings.software.title' }
];

// The bar and what is under it: the overview, or a settings page. tab is the
// settings page shown, for the bar to mark its section. locked: that page is
// at work and must stay, so the bar leads nowhere until it is done.
export const OverviewShell = ({
  tab,
  locked = false,
  children
}: {
  tab?: string;
  locked?: boolean;
  children: ReactNode;
}) => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const navigate = useNavigate();
  const branding = useAtomValue(brandingAtom);
  const webTitle = useAtomValue(webTitleAtom);
  const [request, setRequest] = useAtom(settingsRequestAtom);
  const [atxEnabled, setAtxEnabled] = useAtom(atxEnabledAtom);
  const section = tab === undefined ? undefined : (settingsParentOf(tab) ?? tab);

  // The capture and ATX states the pages and the bar show.
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

  // A link to a settings page from anywhere (a panel, a notice) opens it as a
  // page here; over the remote screen the same request opens the dialog.
  // While the page is locked the request waits, as it does for the dialog.
  useEffect(() => {
    if (!request || locked) return;
    setRequest(null);
    navigate(settingsPath(resolveSettingsTab(request)));
  }, [request, locked, setRequest, navigate]);

  // What leads away from the page, switched off while it is locked.
  const way = locked
    ? ({
        'aria-disabled': true,
        tabIndex: -1,
        onClick: (event: MouseEvent) => event.preventDefault()
      } as const)
    : {};
  const wayClass = locked ? 'pointer-events-none opacity-50' : '';

  const settingsLabel = t('settings.title');

  return (
    <div className="bg-page text-fg flex h-dvh flex-col">
      <header className="border-line shrink-0 border-b bg-neutral-900">
        <div className="flex h-14 items-center gap-3 px-4">
          <Link
            to="/overview"
            {...way}
            className={clsx('flex min-w-0 items-center gap-3 no-underline', wayClass)}
          >
            <img src={brandingFavicon(branding)} alt="" className="size-8 shrink-0" />
            <span className="text-fg-strong truncate text-lg font-semibold">
              {webTitle || 'NanoKVM OS'}
            </span>
          </Link>
          {admin && (
            <nav
              aria-label={t('overview.sections')}
              className="ml-4 hidden min-w-0 items-center gap-1 overflow-x-auto md:flex"
            >
              {sections.map((item) => (
                <Link
                  key={item.id}
                  to={settingsPath(item.tab)}
                  aria-current={section === item.id ? 'page' : undefined}
                  {...way}
                  className={clsx(
                    'text-fg hover:text-fg-strong aria-[current=page]:text-fg-strong rounded px-2.5 py-1.5 text-sm whitespace-nowrap no-underline hover:bg-neutral-700/70 aria-[current=page]:bg-neutral-700/70',
                    wayClass
                  )}
                >
                  {t(item.title)}
                </Link>
              ))}
            </nav>
          )}
          <div className="ml-auto flex shrink-0 items-center gap-1">
            {admin && <Capture />}
            {admin && <Terminal />}
            {atxEnabled && <Power />}
            <ThemeToggle />
            <MenuTooltip title={settingsLabel}>
              <Link
                to="/settings"
                aria-label={settingsLabel}
                {...way}
                className={clsx(
                  'hover:text-fg-strong flex h-[30px] w-[30px] items-center justify-center rounded text-neutral-300 hover:bg-neutral-700/80',
                  wayClass
                )}
              >
                <SettingsIcon size={18} />
              </Link>
            </MenuTooltip>
            <div inert={locked} className={wayClass}>
              <Logout />
            </div>
          </div>
        </div>
      </header>
      <div className="min-h-0 flex-1">{children}</div>
    </div>
  );
};
