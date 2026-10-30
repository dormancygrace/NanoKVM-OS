import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Collapse, message, Popconfirm, Spin, Switch } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { setUpdateNoticeEnabled, type Components, type UpdateNotice } from '@/api/updates.ts';
import { formatDeviceTime } from '@/lib/date-time.ts';
import { componentLabel, parseUpgrades } from '@/lib/update-components.ts';
import { formatVersion } from '@/lib/version';
import { timePreferencesAtom } from '@/hooks/useDeviceTime.ts';
import { Panel, SettingRow, StatusBadge } from '@/components/ui/settings.tsx';

export type APKState = {
  state: string;
  message?: string;
  upgrades?: string;
  log?: string;
  reboot: boolean;
  kernel: string;
};

// Everything about getting the next version, at the top of the page: the
// installed version, what the last check found, Check now and Install, the
// restart a kernel update needs, the log and the daily check.
// "Check now" checks both the packages and the NanoKVM OS repository.
export const UpdateCard = ({
  version,
  apk,
  notice,
  components,
  busy,
  action,
  onChange
}: {
  version?: string;
  apk?: APKState;
  notice?: UpdateNotice;
  components?: Components;
  busy: boolean;
  action: (name: string) => Promise<void>;
  onChange: () => Promise<void>;
}) => {
  const { t, i18n } = useTranslation();
  const tr = useCallback(
    (key: string, options?: Record<string, string>) => t(`settings.updates.${key}`, options),
    [t]
  );
  const timePreferences = useAtomValue(timePreferencesAtom);
  const [switching, setSwitching] = useState(false);

  // Say when a check or an install has just finished.
  const previous = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (previous.current && previous.current !== apk?.state) {
      if (apk?.state === 'installed') message.success(tr('apk.installed'));
      if (apk?.state === 'up-to-date') message.success(tr('apk.current'));
    }
    previous.current = apk?.state;
  }, [apk?.state, tr]);

  const working = busy || ['checking', 'installing', 'rebooting'].includes(apk?.state || '');
  const checking = apk?.state === 'checking' || notice?.state === 'checking';
  // The package check's list when it has one, else the repository check's.
  const upgrades =
    apk?.state === 'ready' && apk.upgrades ? parseUpgrades(apk.upgrades) : (notice?.updates ?? []);
  const known = !!components?.packages.some((component) => component.available);
  const installable = apk?.state === 'ready' || known || upgrades.length > 0;
  const upToDate = upgrades.length === 0 && (apk?.state === 'up-to-date' || notice?.state === 'ok');
  const checked = notice?.checkedAt
    ? formatDeviceTime(notice.checkedAt * 1000, timePreferences, i18n.language, true)
    : undefined;

  async function setDaily(enabled: boolean) {
    setSwitching(true);
    try {
      const rsp = await setUpdateNoticeEnabled(enabled);
      if (rsp.code !== 0) message.error(rsp.msg);
      await onChange();
    } catch {
      message.error(tr('requestFailed'));
    } finally {
      setSwitching(false);
    }
  }

  return (
    <Panel className="space-y-4">
      <div>
        <div className="text-lg">
          {componentLabel(t, 'nanokvm')} {version ? formatVersion(version) : '—'}
        </div>
        <div className="text-fg-muted mt-1 flex flex-wrap items-center gap-2 text-sm">
          {apk?.state === 'installing' ? (
            <>
              <Spin size="small" />
              {tr('status.installing')}
            </>
          ) : checking ? (
            <>
              <Spin size="small" />
              {tr('status.checking')}
            </>
          ) : upgrades.length > 0 ? (
            <StatusBadge tone="info">{tr('status.available')}</StatusBadge>
          ) : upToDate ? (
            <StatusBadge tone="success">{tr('notice.upToDate')}</StatusBadge>
          ) : null}
          <span>
            {notice?.state === 'unavailable'
              ? tr('notice.unavailable')
              : checked
                ? tr('notice.checked', { time: checked })
                : tr('notice.never')}
          </span>
        </div>
      </div>

      {notice?.state === 'failed' && notice.message && (
        <Alert type="warning" showIcon message={tr('notice.failed', { message: notice.message })} />
      )}
      {apk?.state === 'failed' && (
        <Alert type="error" showIcon message={apk.message || tr('apk.failed')} />
      )}

      {upgrades.length > 0 && (
        <table className="w-full border-collapse text-sm">
          <tbody>
            {upgrades.map((upgrade) => (
              <tr key={upgrade.name} className="border-t border-neutral-700/60 first:border-t-0">
                <td className="py-1.5 pr-3">
                  <div>{componentLabel(t, upgrade.name)}</div>
                  <div className="text-fg-muted text-xs">{upgrade.name}</div>
                </td>
                <td className="py-1.5 text-right whitespace-nowrap tabular-nums">
                  {upgrade.installed && `${upgrade.installed} → `}
                  {upgrade.available}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <div className="flex flex-wrap gap-2">
        <Button
          disabled={working}
          loading={apk?.state === 'checking'}
          onClick={() => action('apk/check')}
        >
          {tr('apk.check')}
        </Button>
        {installable && (
          <Popconfirm
            title={tr('apk.confirm')}
            onConfirm={() => action('apk/install')}
            disabled={working}
          >
            <Button type="primary" loading={apk?.state === 'installing'} disabled={working}>
              {tr('apk.install')}
            </Button>
          </Popconfirm>
        )}
      </div>

      {apk?.reboot && (
        <Alert
          type="warning"
          showIcon
          message={tr('apk.rebootRequired')}
          action={
            <Popconfirm title={tr('apk.rebootConfirm')} onConfirm={() => action('apk/reboot')}>
              <Button disabled={working}>{tr('apk.reboot')}</Button>
            </Popconfirm>
          }
        />
      )}

      {apk?.log && (
        <Collapse
          ghost
          items={[
            {
              key: 'log',
              label: tr('apk.log'),
              children: (
                <pre className="max-h-64 overflow-auto text-xs whitespace-pre-wrap">{apk.log}</pre>
              )
            }
          ]}
        />
      )}

      <div className="border-line border-t pt-3">
        <SettingRow
          label={tr('notice.auto')}
          description={tr('notice.autoDescription')}
          htmlFor="update-check"
        >
          <Switch
            id="update-check"
            aria-describedby="update-check-description"
            checked={!!notice?.enabled}
            loading={switching}
            disabled={!notice || switching}
            onChange={(enabled) => void setDaily(enabled)}
          />
        </SettingRow>
      </div>
    </Panel>
  );
};
