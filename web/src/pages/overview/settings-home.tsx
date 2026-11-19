import type { ReactNode } from 'react';
import { Input } from 'antd';
import clsx from 'clsx';
import { ChevronRightIcon, SearchIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import type { SettingsFrame, SettingsFrameEntry } from '@/pages/desktop/menu/settings';
import { settingsParentOf } from '@/pages/desktop/menu/settings/tabs.ts';

import { settingsGroupPath } from './paths.ts';

export const SearchField = ({
  value,
  onChange,
  className
}: {
  value: string;
  onChange: (value: string) => void;
  className?: string;
}) => {
  const { t } = useTranslation();
  return (
    <Input
      allowClear
      type="search"
      className={className}
      prefix={<SearchIcon size={15} className="text-fg-muted" />}
      placeholder={t('overview.search')}
      aria-label={t('overview.search')}
      value={value}
      onChange={(event) => onChange(event.target.value)}
    />
  );
};

type Row = { id: string; title: string; sub?: string; icon?: ReactNode };

// Pages as a list. card: in a panel of its own, with rows for a finger.
const PageList = ({
  rows,
  card = false,
  locked,
  onOpen
}: {
  rows: Row[];
  card?: boolean;
  locked: boolean;
  onOpen: (id: string) => void;
}) => {
  const { t } = useTranslation();
  if (rows.length === 0)
    return <p className="text-fg-muted m-0 px-3 py-2 text-sm">{t('overview.nothingFound')}</p>;
  return (
    <div
      className={clsx(
        'flex flex-col',
        card && 'border-line bg-surface divide-line max-w-[600px] divide-y rounded-xl border'
      )}
    >
      {rows.map((row) => (
        <button
          key={row.id}
          type="button"
          disabled={locked}
          className={clsx(
            'text-fg flex w-full cursor-pointer items-center gap-3 border-0 bg-transparent px-3 text-left text-sm hover:bg-neutral-700/50 disabled:cursor-not-allowed disabled:opacity-50',
            card ? 'min-h-12 first:rounded-t-xl last:rounded-b-xl' : 'min-h-9 rounded-lg'
          )}
          onClick={() => onOpen(row.id)}
        >
          {row.icon}
          <span className="min-w-0 flex-1 truncate">{row.title}</span>
          {row.sub && <span className="text-fg-muted shrink-0 text-xs">{row.sub}</span>}
          {card && <ChevronRightIcon size={14} className="text-fg-muted shrink-0" />}
        </button>
      ))}
    </div>
  );
};

// The pages whose name, or whose group's name, has the text: every page this
// device and this user have, the ones opened from a list included.
export const SearchResults = ({
  frame,
  query,
  card,
  onOpen
}: {
  frame: SettingsFrame;
  query: string;
  card?: boolean;
  onOpen: (id: string) => void;
}) => {
  const text = query.trim().toLowerCase();
  const parentTitle = (entry: SettingsFrameEntry) => {
    const parent = settingsParentOf(entry.id);
    return parent ? frame.title(parent) : '';
  };
  const rows = frame.entries
    .filter((entry) => !entry.isGroup && entry.id !== 'dashboard')
    .map((entry) => ({ id: entry.id, title: entry.title, sub: parentTitle(entry) }))
    .filter((row) => `${row.title} ${row.sub}`.toLowerCase().includes(text));
  return <PageList rows={rows} card={card} locked={frame.locked} onOpen={onOpen} />;
};

// The settings without a page open: every group and page as a tile, or the
// pages of one group as a list.
export const SettingsHome = ({ frame, group }: { frame: SettingsFrame; group?: string }) => {
  const navigate = useNavigate();
  const children = (id: string) =>
    frame.entries.filter((entry) => entry.group === id && !entry.hidden);

  if (group)
    return (
      <PageList
        card
        rows={children(group).map((entry) => ({ id: entry.id, title: entry.title }))}
        locked={frame.locked}
        onOpen={frame.open}
      />
    );

  return (
    <div className="grid max-w-[1100px] grid-cols-[repeat(auto-fill,minmax(min(260px,100%),1fr))] gap-3">
      {frame.entries
        .filter((entry) => !entry.group && !entry.hidden && entry.id !== 'dashboard')
        .map((entry) => (
          <button
            key={entry.id}
            type="button"
            disabled={frame.locked}
            className="border-line bg-surface text-fg flex min-h-16 cursor-pointer items-center gap-3 rounded-xl border px-3.5 py-2.5 text-left hover:bg-neutral-700/50 disabled:cursor-not-allowed disabled:opacity-50"
            onClick={() =>
              entry.isGroup ? navigate(settingsGroupPath(entry.id)) : frame.open(entry.id)
            }
          >
            <span className="text-info flex size-9 shrink-0 items-center justify-center rounded-lg bg-neutral-700/50">
              {entry.icon}
            </span>
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="truncate text-sm font-medium">{entry.title}</span>
              {entry.isGroup && (
                <span className="text-fg-muted truncate text-xs">
                  {children(entry.id)
                    .map((child) => child.title)
                    .join(', ')}
                </span>
              )}
            </span>
            <ChevronRightIcon size={14} className="text-fg-muted shrink-0" />
          </button>
        ))}
    </div>
  );
};
