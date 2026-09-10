import { useEffect, useState } from 'react';
import { Alert, Button, message } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';

import { UpdateNoticeTracker } from './notice';

const updateNotice = new UpdateNoticeTracker();

type State = {
  installed: { version: string; sequence: number };
  check: {
    checking: boolean;
    checked_at: string;
    error: string;
    release: { version: string; url: string } | null;
  };
  operation: { state: string; message: string; version?: string; id?: string };
};

export const Updates = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<State | null>(null);
  const [busy, setBusy] = useState(false);
  const [reconnecting, setReconnecting] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [progress, setProgress] = useState(0);

  async function refresh() {
    try {
      const rsp = await http.get('/api/os/update');
      if (rsp.code === 0) {
        updateNotice.observe(rsp.data.operation);
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
    if (file.size > 96 * 1024 * 1024) {
      message.error(t('settings.updates.tooLarge'));
      return;
    }
    setBusy(true);
    setProgress(0);
    try {
      const rsp = await http.post('/api/os/update/upload', file, {
        headers: { 'Content-Type': 'application/octet-stream' },
        timeout: 10 * 60 * 1000,
        onUploadProgress: (event) =>
          setProgress(Math.round((100 * event.loaded) / (event.total || file.size)))
      });
      if (rsp.code !== 0) message.error(rsp.msg);
      await refresh();
    } catch {
      message.error(t('settings.updates.requestFailed'));
    } finally {
      setBusy(false);
    }
  }
  const operation = state?.operation;
  const showOperation = operation && updateNotice.visible(operation);
  const working = busy || operation?.state === 'downloading' || operation?.state === 'installing';
  return (
    <div className="flex flex-col gap-4">
      <h2 className="text-xl">{t('settings.updates.title')}</h2>
      <p className="text-sm text-neutral-400">{t('settings.updates.description')}</p>
      <div>
        {t('settings.updates.installed')}: {state?.installed.version || '—'}
      </div>
      <p className="text-sm text-neutral-400">{t('settings.updates.automatic')}</p>
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
            {t('settings.updates.latest')}: {state.check.release.version}
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
      <Button disabled={working || !file} onClick={upload}>
        {t('settings.updates.verify')}
        {busy && progress > 0 ? ` (${progress}%)` : ''}
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
          {t('settings.updates.install')} {operation.version}
        </Button>
      )}
      {reconnecting && <Alert type="info" message={t('settings.updates.reconnecting')} />}
      {showOperation && operation?.state === 'installed' && (
        <Button onClick={() => window.location.reload()}>{t('settings.updates.reload')}</Button>
      )}
      <p className="text-sm text-neutral-400">{t('settings.updates.compatibility')}</p>
    </div>
  );
};
