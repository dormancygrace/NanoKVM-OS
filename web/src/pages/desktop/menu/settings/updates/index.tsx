import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Collapse, message, Popconfirm, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { formatVersion } from '@/lib/version';

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
    <div className="flex flex-col gap-4">
      <p className="text-sm text-neutral-400">{t('settings.updates.description')}</p>
      <div>
        {t('settings.updates.installed')}:{' '}
        {state?.installed.version ? formatVersion(state.installed.version) : '—'}
      </div>
      {state && <APKUpdates state={state.apk} busy={busy || alpineWorking} action={action} />}
      {state?.alpine?.enabled && (
        <div className="flex flex-col gap-4 rounded-lg border border-neutral-700 p-4">
          <div className="font-medium">{t('settings.updates.alpineTitle')}</div>
          <p className="text-sm text-neutral-400">{t('settings.updates.alpineDisclaimer')}</p>
          <Collapse
            ghost
            expandIconPosition="end"
            items={[
              {
                key: 'image-options',
                label: t('settings.updates.alpineOptions'),
                children: (
                  <div className="flex flex-col gap-4 pb-2">
                    <div className="flex flex-col gap-2">
                      <label htmlFor="image-profile" className="text-sm">
                        {t('settings.updates.alpineProfile')}
                      </label>
                      <Select
                        id="image-profile"
                        value={alpineProfile}
                        disabled={busy || working || alpineWorking}
                        onChange={setAlpineProfile}
                        options={[
                          { value: 'stock', label: 'stock' },
                          { value: 'c906-scalar', label: 'c906-scalar' }
                        ]}
                      />
                    </div>
                    <div className="flex flex-col gap-2">
                      <label htmlFor="image-packages" className="text-sm">
                        {t('settings.updates.alpinePackages')}
                      </label>
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
                    </div>
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
        </div>
      )}
      {state && !state.alpine.enabled && (
        <Alert type="info" message={t('settings.updates.alpineBuilderMissing')} />
      )}
      {reconnecting && <Alert type="info" message={t('settings.updates.reconnecting')} />}
    </div>
  );
};
