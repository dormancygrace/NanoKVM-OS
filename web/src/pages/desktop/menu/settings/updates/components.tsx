import { useState } from 'react';
import { Alert, message, Spin, Switch, Tag } from 'antd';
import { useTranslation } from 'react-i18next';

import { setUpdateNoticeEnabled, type Components, type UpdateNotice } from '@/api/updates.ts';
import { componentLabel } from '@/lib/update-components.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

// The installed NanoKVM OS packages, with the newer version the last check
// found, and the Alpine and kernel releases.
export const ComponentList = ({ components }: { components?: Components }) => {
  const { t } = useTranslation();
  const tr = (key: string, options?: Record<string, string>) =>
    t(`settings.updates.components.${key}`, options);
  const kernelChanges =
    !!components?.bootKernel && !!components.kernel && components.bootKernel !== components.kernel;
  return (
    <Panel className="space-y-3">
      <div className="font-medium">{tr('title')}</div>
      <p className="text-fg-muted mt-0 text-sm">{tr('description')}</p>
      <table className="w-full border-collapse text-sm">
        <tbody>
          {components?.packages.map((component) => (
            <tr key={component.name} className="border-t border-neutral-700/60 first:border-t-0">
              <td className="py-1.5 pr-3">
                <div>{componentLabel(t, component.name)}</div>
                <div className="text-fg-muted text-xs">{component.name}</div>
              </td>
              <td className="py-1.5 text-right whitespace-nowrap tabular-nums">
                {component.installed}
                {component.available && (
                  <Tag color="blue" className="ms-2 me-0">
                    {tr('available', { version: component.available })}
                  </Tag>
                )}
              </td>
            </tr>
          ))}
          <tr className="border-t border-neutral-700/60">
            <td className="py-1.5 pr-3">{tr('alpine')}</td>
            <td className="py-1.5 text-right tabular-nums">{components?.alpine || '—'}</td>
          </tr>
          <tr className="border-t border-neutral-700/60">
            <td className="py-1.5 pr-3">{tr('kernel')}</td>
            <td className="py-1.5 text-right tabular-nums">
              {components?.kernel || '—'}
              {kernelChanges && (
                <div className="text-fg-muted text-xs">
                  {tr('kernelNext', { version: components.bootKernel })}
                </div>
              )}
            </td>
          </tr>
        </tbody>
      </table>
    </Panel>
  );
};

// The daily look at the NanoKVM OS repository that drives the notification.
export const UpdateCheck = ({
  notice,
  onChange
}: {
  notice?: UpdateNotice;
  onChange: () => Promise<void>;
}) => {
  const { t, i18n } = useTranslation();
  const tr = (key: string, options?: Record<string, string>) =>
    t(`settings.updates.notice.${key}`, options);
  const [busy, setBusy] = useState(false);
  const checking = notice?.state === 'checking';

  async function run(request: () => Promise<{ code: number; msg: string }>) {
    setBusy(true);
    try {
      const rsp = await request();
      if (rsp.code !== 0) message.error(rsp.msg);
      await onChange();
    } catch {
      message.error(t('settings.updates.requestFailed'));
    } finally {
      setBusy(false);
    }
  }

  const checked = notice?.checkedAt
    ? new Date(notice.checkedAt * 1000).toLocaleString(i18n.language)
    : undefined;
  return (
    <Panel className="space-y-3">
      <SettingRow label={tr('auto')} description={tr('autoDescription')} htmlFor="update-check">
        <Switch
          id="update-check"
          checked={!!notice?.enabled}
          loading={busy && !checking}
          disabled={!notice}
          onChange={(enabled) => run(() => setUpdateNoticeEnabled(enabled))}
        />
      </SettingRow>
      {/* Checked from Package updates: one check covers both repositories. */}
      <div className="text-fg-muted flex items-center gap-2 text-sm">
        {notice?.state === 'unavailable'
          ? tr('unavailable')
          : checked
            ? tr('checked', { time: checked })
            : tr('never')}
        {checking && <Spin size="small" />}
      </div>
      {notice?.state === 'ok' && notice.updates.length === 0 && (
        <Alert type="success" showIcon message={tr('upToDate')} />
      )}
      {notice?.state === 'failed' && notice.message && (
        <Alert type="warning" showIcon message={tr('failed', { message: notice.message })} />
      )}
    </Panel>
  );
};
