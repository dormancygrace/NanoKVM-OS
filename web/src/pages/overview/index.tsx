import { useCallback, useEffect, useRef, useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button } from 'antd';
import clsx from 'clsx';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { ArrowLeftIcon, LayoutDashboardIcon, MonitorIcon, SettingsIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
  Link,
  useBlocker,
  useLocation,
  useMatch,
  useNavigate,
  useSearchParams
} from 'react-router-dom';

import { adoptLiveCapture } from '@/lib/capture-control.ts';
import { subscribeLiveStatus } from '@/lib/live-status.ts';
import { atxEnabledAtom, atxLocalChange } from '@/jotai/atx.ts';
import { brandingAtom, brandingFavicon } from '@/jotai/branding';
import { captureReadyAtom, isHdmiEnabledAtom } from '@/jotai/screen.ts';
import { settingsRequestAtom, webTitleAtom } from '@/jotai/settings.ts';
import { useMediaQuery } from '@/hooks/useMediaQuery.ts';
import { Head } from '@/components/head.tsx';
import { ThemeToggle } from '@/components/theme-toggle.tsx';
import { Panel } from '@/components/ui/settings.tsx';
import { Power } from '@/pages/desktop/menu/power';
import { Settings, type SettingsFrame } from '@/pages/desktop/menu/settings';
import { Dashboard } from '@/pages/desktop/menu/settings/dashboard';
import { SessionActions } from '@/pages/desktop/menu/settings/session-actions.tsx';
import {
  isSettingsGroup,
  resolveSettingsTab,
  settingsGroupOf
} from '@/pages/desktop/menu/settings/tabs.ts';

import { CaptureSwitch, HostActions, HostCard, HostState } from './host.tsx';
import { cameFrom, cameFromOf, settingsGroupPath, settingsPath, wayOf } from './paths.ts';
import { ScreenPreview } from './screen-preview.tsx';
import { SearchField, SearchResults, SettingsHome } from './settings-home.tsx';
import { Sidebar } from './sidebar.tsx';

type View = 'overview' | 'status' | 'settings';

// The state of the device and of the host. Beside the navigation it is one
// page. Where the navigation is at the bottom it is two: the host and what is
// done to it (overview), and the device (status).
const Overview = ({
  wide,
  view,
  admin,
  locked,
  open
}: {
  wide: boolean;
  view: View;
  admin: boolean;
  locked: boolean;
  open: (tab: string) => void;
}) => {
  const { t } = useTranslation();
  // With the capture switched off there is no screen to show: its panel is
  // left out, like the other panels of what is not in use.
  const captureReady = useAtomValue(captureReadyAtom);
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  const captureOff = captureReady && !captureEnabled;

  return (
    <div className="flex max-w-[1400px] flex-col gap-4">
      {(wide || view === 'overview') && (
        <div className="flex flex-wrap items-start gap-4">
          {!captureOff && (
            <div className="w-full md:w-[380px] md:shrink-0">
              <ScreenPreview />
            </div>
          )}
          {/* Also with the capture off: the console says so, and someone who
              may not switch it on still gets there. */}
          {!wide && (
            <Link to="/screen" className="w-full">
              <Button type="primary" size="large" block icon={<MonitorIcon size={18} />}>
                {t('overview.screen.openScreen')}
              </Button>
            </Link>
          )}
          {!wide && <HostActions admin={admin} locked={locked} />}
          {!wide && admin && (
            <Panel className="w-full py-2!">
              <CaptureSwitch className="min-h-9 justify-between" />
            </Panel>
          )}
          <HostCard className="w-full md:w-[380px]" />
        </div>
      )}
      {(wide || view === 'status') && <Dashboard navigate={open} caption={false} />}
    </div>
  );
};

// Every page but the remote screen: beside the navigation on a wide window,
// between a title and three tabs on a narrow one. The settings hand over
// their entries and the open page (frame); tab is the settings page in the
// address.
const Layout = ({ frame, tab }: { frame: SettingsFrame; tab?: string }) => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const navigate = useNavigate();
  const { pathname, search, state } = useLocation();
  const [params] = useSearchParams();
  const wide = useMediaQuery('(min-width: 768px)');
  const branding = useAtomValue(brandingAtom);
  const webTitle = useAtomValue(webTitleAtom);
  const atx = useAtomValue(atxEnabledAtom) === true;
  const [query, setQuery] = useState('');
  const scroll = useRef<HTMLElement>(null);

  const view: View = pathname.startsWith('/settings')
    ? 'settings'
    : pathname === '/status'
      ? 'status'
      : 'overview';
  const locked = frame.locked;
  const way = wayOf(locked);
  // a settings page is open
  const page = view === 'settings' && tab !== undefined;
  const active = page ? frame.current : undefined;
  const asked = params.get('g');
  const group =
    view === 'settings' && !page
      ? frame.entries.find((entry) => entry.isGroup && entry.id === asked)?.id
      : undefined;
  const searching = query.trim() !== '';
  // Without the navigation beside it, the search shows what it finds in the
  // page.
  const results = !wide && view === 'settings' && !page && searching;

  useEffect(() => {
    scroll.current?.scrollTo({ top: 0, left: 0 });
  }, [pathname, search]);

  function openFound(id: string) {
    setQuery('');
    frame.open(id);
  }

  // Back from a settings page or a group. A page opened from the overview or
  // the status returns there. Otherwise one step up: the list a collection
  // item came from, the pages of its group, the settings.
  const from = page && !wide ? cameFromOf(state) : undefined;
  const back = from
    ? () => navigate(-1)
    : !page
      ? group
        ? () => navigate('/settings')
        : undefined
      : frame.collection
        ? () => frame.open(frame.collection!)
        : wide
          ? undefined
          : () => {
              const parent = settingsGroupOf(frame.current);
              navigate(parent ? settingsGroupPath(parent) : '/settings');
            };
  const title =
    view === 'settings'
      ? page
        ? wide
          ? frame.pageTitle(frame.current)
          : frame.title(frame.current)
        : group
          ? frame.title(group)
          : t('settings.title')
      : view === 'status' && !wide
        ? t('overview.status')
        : t('overview.title');

  const backButton = back && (
    <Button
      type="text"
      aria-label={t('settings.back')}
      disabled={locked}
      icon={<ArrowLeftIcon size={18} />}
      onClick={back}
    />
  );

  const tabs: { to: string; view: View; icon: typeof MonitorIcon; title: string }[] = [
    { to: '/overview', view: 'overview', icon: MonitorIcon, title: t('overview.host.title') },
    { to: '/status', view: 'status', icon: LayoutDashboardIcon, title: t('overview.status') },
    { to: '/settings', view: 'settings', icon: SettingsIcon, title: t('settings.title') }
  ];

  return (
    <div
      className={clsx(
        // no flash of the whole control under a finger
        'bg-page text-fg fixed inset-0 flex [-webkit-tap-highlight-color:transparent]',
        wide ? 'flex-row' : 'flex-col'
      )}
    >
      <Head title={view === 'settings' ? t('settings.title') : t('overview.title')} />
      {wide ? (
        <Sidebar
          key="navigation"
          frame={frame}
          active={active}
          overview={view !== 'settings'}
          admin={admin}
          query={query}
          onQuery={setQuery}
          onOpen={openFound}
        />
      ) : (
        <header
          key="title"
          className="border-line bg-panel flex h-13 shrink-0 items-center gap-2 border-b px-3"
        >
          {backButton}
          {view === 'overview' && (
            <img src={brandingFavicon(branding)} alt="" className="size-7 shrink-0" />
          )}
          <span className="text-fg-strong min-w-0 flex-1 truncate text-base font-semibold">
            {view === 'overview' ? webTitle || 'NanoKVM OS' : title}
          </span>
          <HostState />
          <ThemeToggle />
        </header>
      )}
      <div key="page" className="flex min-h-0 min-w-0 flex-1 flex-col">
        {wide && (
          <header className="border-line bg-panel flex h-14 shrink-0 items-center gap-4 border-b px-6">
            {backButton}
            <h1 className="text-fg-strong m-0 min-w-0 flex-1 truncate text-lg font-semibold">
              {title}
            </h1>
            <HostState />
            {admin && <CaptureSwitch />}
            {atx && (
              <Power
                label
                placement="bottomRight"
                className="border-line text-fg min-h-8 rounded-lg border px-3"
              />
            )}
          </header>
        )}
        <main ref={scroll} className="min-h-0 flex-1 overflow-y-auto p-4 md:p-6">
          {view !== 'settings' && (
            <Overview wide={wide} view={view} admin={admin} locked={locked} open={frame.open} />
          )}
          {view === 'settings' && !page && (
            <div className="flex flex-col gap-4">
              {!wide && <SearchField value={query} onChange={setQuery} />}
              {results ? (
                <SearchResults card frame={frame} query={query} onOpen={openFound} />
              ) : (
                <SettingsHome frame={frame} group={group} />
              )}
              {!wide && !results && !group && (
                <div className="max-w-[600px]">
                  <SessionActions isAdmin={admin} disabled={locked} mobile />
                </div>
              )}
            </div>
          )}
          {page && <div className="mx-auto w-full max-w-[600px]">{frame.content}</div>}
        </main>
      </div>
      {!wide && (
        <nav
          aria-label={t('overview.navigation')}
          className="border-line bg-panel flex shrink-0 border-t pb-[env(safe-area-inset-bottom)]"
        >
          {tabs.map((item) => (
            <Link
              key={item.view}
              to={item.to}
              aria-current={view === item.view ? 'page' : undefined}
              {...way.props}
              className={clsx(
                'text-fg-muted aria-[current=page]:text-info flex h-14 flex-1 flex-col items-center justify-center gap-0.5 text-xs no-underline aria-[current=page]:font-semibold',
                way.className
              )}
            >
              <item.icon size={22} />
              <span>{item.title}</span>
            </Link>
          ))}
        </nav>
      )}
      {/* Out of the way of the controls: the lower corner. A narrow window has
          it in the title line, whose right end is free. */}
      {wide && (
        <div className="fixed right-4 bottom-4 z-10">
          <ThemeToggle />
        </div>
      )}
    </div>
  );
};

// The pages of a device that is managed more than it is watched: the
// overview and the settings, around one navigation. They take no part in the
// video: no stream, no input.
export const Console = () => {
  const navigate = useNavigate();
  const tab = useMatch('/settings/:tab')?.params.tab;
  const [request, setRequest] = useAtom(settingsRequestAtom);
  const setAtxEnabled = useSetAtom(atxEnabledAtom);
  // A page at work locks the navigation of the settings. Leaving it by a
  // link from elsewhere or by the browser's Back would end what it is doing
  // (an installation whose progress it follows) just the same.
  const [locked, setLocked] = useState(false);
  const blocker = useBlocker(locked);
  useEffect(() => {
    if (blocker.state === 'blocked') blocker.reset();
  }, [blocker]);

  // The capture and ATX states the pages show.
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

  // A page opened from the overview or the status remembers it, for Back.
  const { pathname } = useLocation();
  const outside = pathname.startsWith('/settings') ? undefined : pathname;
  // An address that names a group with no page of its own (Services,
  // Software) is the list of its pages.
  const namedGroup = tab !== undefined && isSettingsGroup(resolveSettingsTab(tab));
  useEffect(() => {
    if (namedGroup) navigate(settingsGroupPath(resolveSettingsTab(tab!)), { replace: true });
  }, [namedGroup, tab, navigate]);
  const onTab = useCallback(
    (next: string) =>
      navigate(settingsPath(next), outside ? { state: cameFrom(outside) } : undefined),
    [navigate, outside]
  );
  const onClose = useCallback(() => navigate('/overview'), [navigate]);
  const onOpenScreen = useCallback(() => navigate('/screen'), [navigate]);
  const onUnavailable = useCallback(() => navigate('/settings', { replace: true }), [navigate]);

  return (
    <Settings
      page={{
        tab,
        onTab,
        onClose,
        onOpenScreen,
        onUnavailable,
        onLock: setLocked,
        render: (frame) => <Layout frame={frame} tab={tab} />
      }}
    />
  );
};
