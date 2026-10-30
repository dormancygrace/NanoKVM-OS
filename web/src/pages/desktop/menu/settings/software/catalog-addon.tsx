import { useState } from 'react';
import { Alert, Button, Tag } from 'antd';
import { useAtomValue } from 'jotai';
import {
  DownloadIcon,
  ExternalLinkIcon,
  PackageIcon,
  RefreshCwIcon,
  Trash2Icon
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { addonCatalogAction, type AddonCatalog, type CatalogAddon } from '@/api/addons.ts';
import { formatDeviceTime } from '@/lib/date-time.ts';
import { timePreferencesAtom } from '@/hooks/useDeviceTime.ts';
import { confirmAction } from '@/components/ui/confirm.ts';

import { AddonCard } from './addon-card';

// "nanokvm-serial-tools" -> "Serial tools": a title for an add-on that has no
// page of its own.
const title = (name: string) => {
  const words = name.replace(/^nanokvm-/, '').replace(/[-_]+/g, ' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
};

// The minimum OS release, or nothing when the installed OS is new enough.
export const MinOsNotice = ({ addon, os }: { addon?: CatalogAddon; os?: string }) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.software.addons' });
  if (!addon || addon.compatible || !addon.minOs) return null;
  return (
    <Alert
      type="info"
      showIcon
      title={t('needsOs', { version: addon.minOs, installed: os || '?' })}
    />
  );
};

// A card for an add-on of the repository without a page of its own: what the
// index says of it, and install, update and remove.
export const CatalogAddonCard = ({
  addon,
  os,
  listed,
  onChanged
}: {
  addon: CatalogAddon;
  os?: string;
  // The list came from the repository: an add-on without a version there is
  // no longer offered (before the first check every version is unknown).
  listed: boolean;
  onChanged: (catalog?: AddonCatalog) => void;
}) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.software.addons' });
  const [busy, setBusy] = useState<'install' | 'remove'>();
  const [error, setError] = useState('');

  const run = async (action: 'install' | 'remove') => {
    if (busy) return;
    if (action === 'remove') {
      const confirmed = await confirmAction({
        title: t('removeTitle', { name: title(addon.name) }),
        content: t('removeConfirm'),
        danger: true,
        safeDefault: true
      });
      if (!confirmed) return;
    }
    setBusy(action);
    try {
      const response = await addonCatalogAction(action, addon.name);
      if (response.code !== 0) throw new Error(response.msg);
      setError('');
      onChanged(response.data);
    } catch (err) {
      setError(err instanceof Error ? err.message : t('failed'));
      onChanged();
    } finally {
      setBusy(undefined);
    }
  };

  const update = !!addon.installed && !!addon.version && addon.installed !== addon.version;
  return (
    <AddonCard title={title(addon.name)} icon={<PackageIcon size={24} />}>
      {addon.description && <p className="text-fg m-0 text-sm">{addon.description}</p>}
      <div className="text-fg-muted flex flex-wrap items-center gap-2 text-xs">
        <span>{addon.name}</span>
        {addon.installed ? (
          <Tag className="m-0">{t('installedVersion', { version: addon.installed })}</Tag>
        ) : (
          addon.version && <Tag className="m-0">{t('version', { version: addon.version })}</Tag>
        )}
        {addon.license && <span>{addon.license}</span>}
        {addon.url && (
          <a
            className="text-info inline-flex items-center gap-1 no-underline!"
            href={addon.url}
            target="_blank"
            rel="noreferrer"
          >
            {t('details')} <ExternalLinkIcon size={12} />
          </a>
        )}
      </div>
      {listed && !addon.version && addon.installed && (
        <Alert type="warning" showIcon title={t('notOffered')} />
      )}
      <MinOsNotice addon={addon} os={os} />
      {error && <Alert type="error" title={error} showIcon />}
      <div className="flex flex-wrap gap-2">
        {!addon.installed && addon.version && (
          <Button
            type="primary"
            icon={<DownloadIcon size={16} />}
            loading={busy === 'install'}
            disabled={!addon.compatible || !!busy}
            onClick={() => void run('install')}
          >
            {t('install')}
          </Button>
        )}
        {update && (
          <Button
            type="primary"
            icon={<RefreshCwIcon size={16} />}
            loading={busy === 'install'}
            disabled={!addon.compatible || !!busy}
            onClick={() => void run('install')}
          >
            {t('update', { version: addon.version })}
          </Button>
        )}
        {addon.installed && (
          <Button
            type="text"
            icon={<Trash2Icon size={16} />}
            loading={busy === 'remove'}
            disabled={!!busy}
            onClick={() => void run('remove')}
          >
            {t('remove')}
          </Button>
        )}
      </div>
    </AddonCard>
  );
};

// When the list was taken from the repository, and a check now.
export const CatalogStatus = ({
  catalog,
  onCheck,
  checking
}: {
  catalog?: AddonCatalog;
  onCheck: () => void;
  checking: boolean;
}) => {
  const { t, i18n } = useTranslation();
  const timePreferences = useAtomValue(timePreferencesAtom);
  const tr = (key: string, options?: Record<string, string>) =>
    t(`settings.software.addons.${key}`, options);
  const running = checking || catalog?.state === 'checking';
  const checked = catalog?.checkedAt
    ? tr('listChecked', {
        date: formatDeviceTime(catalog.checkedAt * 1000, timePreferences, i18n.language, true)
      })
    : tr('listNever');
  return (
    <div className="text-fg-muted flex flex-wrap items-center gap-3 text-sm">
      <span>{running ? tr('listChecking') : checked}</span>
      <Button size="small" icon={<RefreshCwIcon size={14} />} loading={running} onClick={onCheck}>
        {tr('checkNow')}
      </Button>
      {catalog && !catalog.noticeEnabled && <span>{tr('dailyOff')}</span>}
      {catalog?.state === 'failed' && <span className="text-danger">{tr('listFailed')}</span>}
    </div>
  );
};
