import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Collapse, message, Popconfirm, Select } from 'antd';
import { LoaderCircle } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { formatVersion } from '@/lib/version';

import { APKUpdates, type APKState } from './apk';
import { UpdateNoticeTracker } from './notice';

const updateNotice = new UpdateNoticeTracker();

type State = {
  apk?: APKState;
  installed: { version: string; sequence: number };
  check: {
    checking: boolean;
    checked_at: string;
    error: string;
    release: { version: string; url: string } | null;
  };
  operation: { state: string; message: string; version?: string; id?: string; reboot?: boolean };
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
  const [file, setFile] = useState<File | null>(null);
  const [progress, setProgress] = useState(0);
  const [uploading, setUploading] = useState(false);
  const [alpineProfile, setAlpineProfile] = useState('stock');
  const [alpinePackages, setAlpinePackages] = useState('');
  const alpineDefaultsLoaded = useRef(false);
  const previousAlpineState = useRef<string | undefined>(undefined);

  async function refresh() {
    try {
      const rsp = await http.get('/api/os/update');
      if (rsp.code === 0) {
        updateNotice.observe(rsp.data.operation);
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
          setAlpineProfile(current.profile === 'c906-scalar' ? 'c906-scalar' : 'stock');
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
    const timer = setInterval(refresh, 3000);
    return () => clearInterval(timer);
  }, []);

  async function action(name: string, data?: unknown) {
    const installId = name === 'install' ? state?.operation.id : undefined;
    updateNotice.expect(installId);
    setBusy(true);
    try {
      const rsp = await http.post(`/api/os/update/${name}`, data);
      if (rsp.code !== 0) {
        updateNotice.forget(installId);
        message.error(rsp.msg);
      } else if (name === 'install') setReconnecting(true);
      await refresh();
    } catch {
      message.error(t('settings.updates.requestFailed'));
    } finally {
      setBusy(false);
    }
  }
  async function upload() {
    if (!file) return;
    if (file.size > 192 * 1024 * 1024) {
      message.error(t('settings.updates.tooLarge'));
      return;
    }
    setBusy(true);
    setUploading(true);
    setProgress(0);
    try {
      const rsp = await http.post('/api/os/update/upload', file, {
        headers: { 'Content-Type': 'application/octet-stream' },
        timeout: 10 * 60 * 1000,
        onUploadProgress: (event) =>
          setProgress(
            Math.min(
              100,
              Math.max(0, Math.floor((100 * event.loaded) / (event.total || file.size)))
            )
          )
      });
      if (rsp.code !== 0) message.error(rsp.msg);
      await refresh();
    } catch {
      message.error(t('settings.updates.requestFailed'));
    } finally {
      setUploading(false);
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
  const operation = state?.operation;
  const verifying = operation?.state === 'verifying' || (uploading && progress === 100);
  const showOperation = operation && updateNotice.visible(operation);
  const working =
    busy ||
    ['checking', 'installing', 'rebooting'].includes(state?.apk?.state || '') ||
    operation?.state === 'downloading' ||
    operation?.state === 'installing' ||
    operation?.state === 'verifying';
  const alpineOperation = state?.alpine?.operation;
  const alpineWorking = ['building', 'staging', 'installing'].includes(
    alpineOperation?.state || ''
  );
  return (
    <div className="flex flex-col gap-4">
      <h2 className="text-xl">{t('settings.updates.title')}</h2>
      <p className="text-sm text-neutral-400">{t('settings.updates.description')}</p>
      <div>
        {t('settings.updates.installed')}:{' '}
        {state?.installed.version ? formatVersion(state.installed.version) : '—'}
      </div>
      {!state?.alpine?.current.profile && (
        <p className="text-sm text-neutral-400">{t('settings.updates.automatic')}</p>
      )}
      {state?.alpine?.current.profile && (
        <APKUpdates state={state.apk} busy={busy || alpineWorking} action={action} />
      )}
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
      {state?.alpine?.current.profile && !state.alpine.enabled && (
        <Alert type="info" message={t('settings.updates.alpineBuilderMissing')} />
      )}
      {!state?.alpine?.current.profile && (
        <>
          <a
            href="https://github.com/dormancygrace/NanoKVM-OS/releases"
            target="_blank"
            rel="noreferrer"
          >
            NanoKVM OS · GitHub Releases
          </a>
          <Button disabled={working || state?.check.checking} onClick={() => action('check')}>
            {t('settings.updates.check')}
          </Button>
          {state?.check.error && <Alert type="info" message={state.check.error} />}
          {state?.check.release && (
            <div className="flex items-center gap-3">
              <span>
                {t('settings.updates.latest')}: {formatVersion(state.check.release.version)}
              </span>
              <Button disabled={working} onClick={() => action('download')}>
                {t('settings.updates.download')}
              </Button>
            </div>
          )}
          <label className="flex flex-col gap-2 text-sm">
            {t('settings.updates.file')}
            <input
              type="file"
              accept=".nkos"
              disabled={working}
              onChange={(event) => setFile(event.target.files?.[0] || null)}
            />
          </label>
          <Button
            className="relative overflow-hidden"
            style={uploading || verifying ? { color: '#e5e5e5' } : undefined}
            disabled={working || !file}
            aria-busy={uploading || verifying}
            onClick={upload}
          >
            {uploading && (
              <span
                aria-hidden="true"
                className="pointer-events-none absolute inset-y-0 left-0 bg-blue-600/40 transition-[width] duration-200 motion-reduce:transition-none"
                style={{ width: `${progress}%` }}
              />
            )}
            <span className="relative z-10 inline-flex items-center justify-center gap-2">
              {verifying && <LoaderCircle size={14} aria-hidden="true" className="animate-spin" />}
              {verifying
                ? t('settings.updates.verifying')
                : uploading
                  ? `${t('settings.updates.uploading')} ${progress}%`
                  : t('settings.updates.verify')}
            </span>
          </Button>
          {showOperation && operation?.message && (
            <Alert
              type={['failed', 'rolled-back'].includes(operation.state) ? 'warning' : 'info'}
              message={operation.message}
            />
          )}
          {operation?.state === 'prepared' && (
            <Button
              type="primary"
              disabled={working}
              onClick={() => action('install', { id: operation.id })}
            >
              {t(operation.reboot ? 'settings.updates.installRestart' : 'settings.updates.install')}{' '}
              {operation.version ? formatVersion(operation.version) : ''}
            </Button>
          )}
          {showOperation && operation?.state === 'installed' && (
            <Button onClick={() => window.location.reload()}>{t('settings.updates.reload')}</Button>
          )}
          <p className="text-sm text-neutral-400">{t('settings.updates.compatibility')}</p>
        </>
      )}
      {reconnecting && <Alert type="info" message={t('settings.updates.reconnecting')} />}
    </div>
  );
};
