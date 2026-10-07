import { useCallback, useEffect, useRef } from 'react';
import { Alert, Button, Collapse, message, Popconfirm } from 'antd';
import { useTranslation } from 'react-i18next';

import { Panel } from '@/components/ui/settings.tsx';

export type APKState = {
  state: string;
  message?: string;
  upgrades?: string;
  log?: string;
  reboot: boolean;
  kernel: string;
};
export const APKUpdates = ({
  state,
  busy,
  action
}: {
  state?: APKState;
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
  return (
    <Panel className="space-y-4">
      <div className="font-medium">{tr('title')}</div>
      <p className="text-fg-muted m-0 text-sm">{tr('description')}</p>
      <div className="text-sm">
        {tr('kernel')}: {state?.kernel || '—'}
      </div>
      {state?.state === 'ready' && state.upgrades && (
        <pre className="text-fg m-0 max-h-48 overflow-auto text-xs whitespace-pre-wrap">
          {state.upgrades}
        </pre>
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
          disabled={working || state?.state !== 'ready'}
        >
          <Button
            type="primary"
            loading={state?.state === 'installing'}
            disabled={working || state?.state !== 'ready'}
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
