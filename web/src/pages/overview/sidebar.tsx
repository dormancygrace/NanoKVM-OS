import type { ReactNode } from 'react';
import clsx from 'clsx';
import { useAtomValue } from 'jotai';
import { ChevronRightIcon, LayoutDashboardIcon, MonitorIcon, ScrollTextIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';

import { brandingAtom, brandingFavicon } from '@/jotai/branding';
import { webTitleAtom } from '@/jotai/settings.ts';
import { DownloadImage } from '@/pages/desktop/menu/download.tsx';
import { Image } from '@/pages/desktop/menu/image';
import type { SettingsFrame } from '@/pages/desktop/menu/settings';
import { SessionActions } from '@/pages/desktop/menu/settings/session-actions.tsx';
import styles from '@/pages/desktop/menu/settings/sidebar.module.css';
import type { SettingsGroup } from '@/pages/desktop/menu/settings/tabs.ts';
import { Terminal } from '@/pages/desktop/menu/terminal';

import { settingsPath, wayOf } from './paths.ts';
import { SearchField, SearchResults } from './settings-home.tsx';

const row = 'box-border min-h-9 w-full rounded-lg px-3';

// The navigation beside every page: the overview, the remote screen and what
// is done to the host, then every page of the settings as one tree. active:
// the settings page that is open, if one is.
export const Sidebar = ({
  frame,
  active,
  overview,
  admin,
  query,
  onQuery,
  onOpen
}: {
  frame: SettingsFrame;
  active?: string;
  overview: boolean;
  admin: boolean;
  query: string;
  onQuery: (query: string) => void;
  // opens a page found by the search
  onOpen: (id: string) => void;
}) => {
  const { t } = useTranslation();
  const branding = useAtomValue(brandingAtom);
  const webTitle = useAtomValue(webTitleAtom);
  const locked = frame.locked;
  const way = wayOf(locked);

  const link = (to: string, icon: ReactNode, title: string, current: boolean) => (
    <Link
      to={to}
      aria-current={current ? 'page' : undefined}
      {...way.props}
      className={clsx(
        styles.item,
        'text-fg flex items-center gap-3 text-sm no-underline',
        row,
        way.className
      )}
    >
      {icon}
      <span className="truncate">{title}</span>
    </Link>
  );

  return (
    <aside className="border-line bg-panel flex w-64 shrink-0 flex-col border-r">
      <Link
        to="/overview"
        {...way.props}
        className={clsx('flex h-14 shrink-0 items-center gap-3 px-4 no-underline', way.className)}
      >
        <img src={brandingFavicon(branding)} alt="" className="size-8 shrink-0" />
        <span className="text-fg-strong truncate text-lg font-semibold">
          {webTitle || 'NanoKVM OS'}
        </span>
      </Link>
      <div className="px-3 pb-3">
        <SearchField value={query} onChange={onQuery} />
      </div>
      <nav
        aria-label={t('overview.sections')}
        className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-3"
      >
        {query.trim() ? (
          <SearchResults frame={frame} query={query} onOpen={onOpen} />
        ) : (
          <>
            {link('/overview', <LayoutDashboardIcon size={18} />, t('overview.title'), overview)}
            {link('/screen', <MonitorIcon size={18} />, t('overview.screen.title'), false)}
            {admin && <Terminal label className={clsx(row, 'text-fg')} placement="rightTop" />}
            <Image label className={clsx(row, 'text-fg')} />
            {admin && <DownloadImage label className={clsx(row, 'text-fg')} placement="rightTop" />}
            {admin &&
              link(
                settingsPath('system-events'),
                <ScrollTextIcon size={18} />,
                t('settings.events.title'),
                active === 'system-events'
              )}
            <Link
              to="/settings"
              {...way.props}
              className={clsx(
                'text-fg-muted hover:text-fg block px-3 pt-4 pb-1.5 text-xs font-semibold tracking-wide uppercase no-underline',
                way.className
              )}
            >
              {t('settings.title')}
            </Link>
            {frame.entries
              .filter(
                (entry) =>
                  entry.id !== 'dashboard' &&
                  !entry.hidden &&
                  (!entry.group || frame.expanded.has(entry.group))
              )
              .map((entry) => {
                const expanded = entry.isGroup
                  ? frame.expanded.has(entry.id as SettingsGroup)
                  : undefined;
                const current =
                  active !== undefined && (active === entry.id || frame.collection === entry.id);
                return (
                  <button
                    key={entry.id}
                    type="button"
                    disabled={locked}
                    aria-current={current ? 'page' : undefined}
                    aria-expanded={expanded}
                    className={clsx(
                      styles.item,
                      'text-fg flex items-center gap-3 text-left text-sm',
                      entry.group ? 'ml-5 min-h-9 w-[calc(100%-1.25rem)] rounded-lg px-3' : row
                    )}
                    onClick={() => frame.open(entry.id)}
                  >
                    <span className="relative flex size-[18px] shrink-0 items-center justify-center">
                      {entry.icon}
                      {entry.marked && (
                        <span
                          aria-hidden
                          className="absolute -top-0.5 -right-0.5 size-2 rounded-full bg-blue-500"
                        />
                      )}
                    </span>
                    <span className="min-w-0 flex-1 truncate">{entry.title}</span>
                    {expanded !== undefined && (
                      <ChevronRightIcon
                        size={12}
                        className={clsx('shrink-0 transition-transform', expanded && 'rotate-90')}
                      />
                    )}
                  </button>
                );
              })}
          </>
        )}
      </nav>
      <div className="px-2 pb-2">
        <SessionActions isAdmin={admin} disabled={locked} mobile={false} />
      </div>
    </aside>
  );
};
