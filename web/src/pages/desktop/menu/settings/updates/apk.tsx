import { useCallback, useEffect, useRef } from 'react';
import { Alert, Button, Collapse, message, Popconfirm } from 'antd';
import { useTranslation } from 'react-i18next';

import { componentLabel, parseUpgrades } from '@/lib/update-components.ts';
import { Panel } from '@/components/ui/settings.tsx';

export type APKState = {
  state: string;
  message?: string;
  upgrades?: string;
  log?: string;
  reboot: boolean;
  kernel: string;
};
// `known` says that the last update check already found NanoKVM OS updates.
// Installing refreshes the indexes itself, so it needs no check here first.
export const APKUpdates = ({
  state,
  known,
  busy,
  action
}: {
  state?: APKState;
  known: boolean;
  busy: boolean;
  action: (name: string) => Promise<void>;
}) => {
  const { t } = useTranslation();
  const tr = useCallback((key: string) => t(`settings.updates.apk.${key}`), [t]);
  const previous = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (previous.current && previous.current !== state?.state) {
      if (state?.state === 'installed') message.success(tr('installed'));
      if (state?.state === 'up-to-date') message.success(tr('current'));
    }
    previous.current = state?.state;
  }, [state?.state, tr]);
  const working = busy || ['checking', 'installing', 'rebooting'].includes(state?.state || '');
  const installable = state?.state === 'ready' || known;
  return (
    <Panel className="space-y-4">
      <div className="font-medium">{tr('title')}</div>
      <p className="text-fg-muted mt-0 text-sm">{tr('description')}</p>
      {state?.state === 'ready' && state.upgrades && (
        <table className="w-full border-collapse text-sm">
          <tbody>
            {parseUpgrades(state.upgrades).map((upgrade) => (
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
          loading={state?.state === 'checking'}
          onClick={() => action('apk/check')}
        >
          {tr('check')}
        </Button>
        <Popconfirm
          title={tr('confirm')}
          onConfirm={() => action('apk/install')}
          disabled={working || !installable}
        >
          <Button
            type="primary"
            loading={state?.state === 'installing'}
            disabled={working || !installable}
          >
            {tr('install')}
          </Button>
        </Popconfirm>
      </div>
      {state?.state === 'failed' && <Alert type="error" message={state.message || tr('failed')} />}
      {state?.reboot && (
        <Alert
          type="warning"
          message={tr('rebootRequired')}
          action={
            <Popconfirm title={tr('rebootConfirm')} onConfirm={() => action('apk/reboot')}>
              <Button disabled={working}>{tr('reboot')}</Button>
            </Popconfirm>
          }
        />
      )}
      {state?.log && (
        <Collapse
          ghost
          items={[
            {
              key: 'log',
              label: tr('log'),
              children: (
                <pre className="max-h-64 overflow-auto text-xs whitespace-pre-wrap">
                  {state.log}
                </pre>
              )
            }
          ]}
        />
      )}
    </Panel>
  );
};
