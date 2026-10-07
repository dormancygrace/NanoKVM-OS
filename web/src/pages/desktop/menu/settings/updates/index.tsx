import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Collapse, message, Popconfirm, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { formatVersion } from '@/lib/version';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

import { APKUpdates, type APKState } from './apk';

type State = {
  apk?: APKState;
  installed: { version: string };
  alpine: {
    enabled: boolean;
    current: { profile: string; apk_world: string };
    operation: {
      state: string;
      message?: string;
      build_id?: string;
      profile?: string;
      packages?: string[];
    };
  };
};

export const Updates = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<State | null>(null);
  const [busy, setBusy] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [alpineProfile, setAlpineProfile] = useState('stock');
  const [alpinePackages, setAlpinePackages] = useState('');
  const alpineDefaultsLoaded = useRef(false);
  const previousAlpineState = useRef<string | undefined>(undefined);

  async function refresh() {
    try {
      const rsp = await http.get('/api/os/update');
      if (rsp.code === 0) {
        const alpineOperation = rsp.data.alpine?.operation;
        if (
          previousAlpineState.current === 'installing' &&
          alpineOperation?.state === 'installed' &&
          alpineOperation.message
        ) {
          message.success(alpineOperation.message, 5);
        }
        previousAlpineState.current = alpineOperation?.state;
        if (!alpineDefaultsLoaded.current && rsp.data.alpine?.current) {
          const current = rsp.data.alpine.current;
          const packages = String(current.apk_world || '')
            .split(/\s+/)
            .map((item) => item.match(/^([a-z0-9][a-z0-9+_.-]{0,127})(?:[<>=~].*)?$/)?.[1])
            .filter((item): item is string => Boolean(item))
            .filter(
              (item) =>
                ![
                  'nanokvm-release',
                  'nanokvm-app',
                  'nanokvm-base',
                  'nanokvm-kernel-sg2002',
                  'nanokvm-kmod-sg2002',
                  'nanokvm-firmware-sg2002'
                ].includes(item)
            );
          // New images use official Alpine packages, including on older tuned devices.
          setAlpineProfile('stock');
          setAlpinePackages(packages.join(' '));
          alpineDefaultsLoaded.current = true;
        }
        setState(rsp.data);
        setReconnecting(false);
      }
    } catch {
      setReconnecting(true);
    }
  }
  useEffect(() => {
    void refresh();
    const stopPolling = pollWhileVisible(refresh, 3000);
    return () => stopPolling();
  }, []);

  async function action(name: string, data?: unknown) {
    setBusy(true);
    try {
      const rsp = await http.post(`/api/os/update/${name}`, data);
      if (rsp.code !== 0) message.error(rsp.msg);
      await refresh();
    } catch {
      message.error(t('settings.updates.requestFailed'));
    } finally {
      setBusy(false);
    }
  }
  async function alpineAction(name: 'build' | 'stage' | 'install') {
    setBusy(true);
    try {
      const data =
        name === 'build'
          ? {
              profile: alpineProfile,
              packages: alpinePackages
                .split(/[\s,]+/)
                .map((item) => item.trim())
                .filter(Boolean)
            }
          : undefined;
      const rsp = await http.post(`/api/os/update/alpine/${name}`, data);
      if (rsp.code !== 0) message.error(rsp.msg);
      else if (name === 'install') setReconnecting(true);
      await refresh();
    } catch {
      message.error(t('settings.updates.requestFailed'));
    } finally {
      setBusy(false);
    }
  }
  const working = busy || ['checking', 'installing', 'rebooting'].includes(state?.apk?.state || '');
  const alpineOperation = state?.alpine?.operation;
  const alpineWorking = ['building', 'staging', 'installing'].includes(
    alpineOperation?.state || ''
  );
  return (
    <div className="space-y-6">
      <p className="text-fg-muted m-0 text-sm">{t('settings.updates.description')}</p>
      <SettingRow label={t('settings.updates.installed')}>
        <span>{state?.installed.version ? formatVersion(state.installed.version) : '—'}</span>
      </SettingRow>
      {state && <APKUpdates state={state.apk} busy={busy || alpineWorking} action={action} />}
      {state?.alpine?.enabled && (
        <Panel className="space-y-4">
          <div className="font-medium">{t('settings.updates.alpineTitle')}</div>
          <p className="text-fg-muted m-0 text-sm">{t('settings.updates.alpineDisclaimer')}</p>
          <Collapse
            ghost
            expandIconPosition="end"
            items={[
              {
                key: 'image-options',
                label: t('settings.updates.alpineOptions'),
                children: (
                  <div className="space-y-4 pb-2">
                    <SettingRow
                      label={t('settings.updates.alpineProfile')}
                      htmlFor="image-profile"
                      stacked
                    >
                      <Select
                        id="image-profile"
                        value={alpineProfile}
                        disabled={busy || working || alpineWorking}
                        onChange={setAlpineProfile}
                        className="w-full"
                        options={[
                          { value: 'stock', label: 'stock' },
                          { value: 'c906-scalar', label: 'c906-scalar' }
                        ]}
                      />
                    </SettingRow>
                    <SettingRow
                      label={t('settings.updates.alpinePackages')}
                      htmlFor="image-packages"
                      stacked
                    >
                      <Select
                        id="image-packages"
                        mode="tags"
                        value={alpinePackages.split(/[\s,]+/).filter(Boolean)}
                        disabled={busy || working || alpineWorking}
                        tokenSeparators={[' ', ',']}
                        placeholder="zstd, iperf3"
                        onChange={(packages: string[]) => setAlpinePackages(packages.join(' '))}
                        className="w-full"
                        open={false}
                      />
                    </SettingRow>
                  </div>
                )
              }
            ]}
          />
          <div className="flex flex-wrap gap-2">
            <Button
              disabled={busy || working || alpineWorking}
              onClick={() => alpineAction('build')}
            >
              {t('settings.updates.alpineBuild')}
            </Button>
            <Button
              disabled={busy || working || alpineOperation?.state !== 'built'}
              onClick={() => alpineAction('stage')}
            >
              {t('settings.updates.alpineStage')}
            </Button>
            <Popconfirm
              title={t('settings.updates.alpineConfirm')}
              onConfirm={() => alpineAction('install')}
              disabled={busy || working || alpineOperation?.state !== 'staged'}
            >
              <Button
                type="primary"
                disabled={busy || working || alpineOperation?.state !== 'staged'}
              >
                {t('settings.updates.alpineInstall')}
              </Button>
            </Popconfirm>
          </div>
          {alpineOperation?.message && alpineOperation.state !== 'installed' && (
            <Alert
              type={alpineOperation.state === 'failed' ? 'error' : 'info'}
              message={alpineOperation.message}
            />
          )}
        </Panel>
      )}
      {state && !state.alpine.enabled && (
        <Alert type="info" message={t('settings.updates.alpineBuilderMissing')} />
      )}
      {reconnecting && <Alert type="info" message={t('settings.updates.reconnecting')} />}
    </div>
  );
};
