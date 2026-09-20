import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, Modal, Popconfirm, Spin, Tabs, message } from 'antd';
import { RefreshCwIcon, SearchIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';

import styles from './mobile-tabs.module.css';

type Package = { name: string; version: string };
type PackageUpdate = { name: string; installed: string; available: string };
type Operation = {
  state: string;
  action?: string;
  package?: string;
  message?: string;
  log?: string;
};
type IndexStatus = {
  state: 'fresh' | 'stale' | 'missing';
  updatedAt?: number;
  ageSeconds?: number;
  repositories: number;
  cached: number;
};
type SoftwareState = { installed: Package[]; operation: Operation; indexes?: IndexStatus };

const protectedPackages = new Set([
  "apk-tools", "busybox", "musl", "openrc", "nanokvm-app", "nanokvm-base",
  "nanokvm-release", "nanokvm-kernel-sg2002", "nanokvm-kmod-sg2002", "nanokvm-firmware-sg2002"
]);

export const Software = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<SoftwareState>();
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<Package[]>([]);
  const [updates, setUpdates] = useState<PackageUpdate[]>([]);
  const [updatesLoading, setUpdatesLoading] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [searchError, setSearchError] = useState('');
  const [updatesError, setUpdatesError] = useState('');
  const [activeTab, setActiveTab] = useState('available');
  const [searchedQuery, setSearchedQuery] = useState('');
  const [searching, setSearching] = useState(false);
  const [busy, setBusy] = useState(false);
  const [actionStarted, setActionStarted] = useState(false);
  const [removal, setRemoval] = useState<{ name: string; preview: string }>();
  const refreshInFlight = useRef(false);
  const updatesInFlight = useRef(false);
  const searchGeneration = useRef(0);
  const operationState = useRef('');

  const working = busy || state?.operation.state === 'running';
  const showOperation = state?.operation.state === 'running' || (actionStarted && (state?.operation.state === 'succeeded' || state?.operation.state === 'failed'));
  const refresh = async () => {
    if (refreshInFlight.current) return;
    refreshInFlight.current = true;
    try {
      const rsp = await http.get('/api/os/update/software');
      if (rsp.code === 0) {
        operationState.current = rsp.data.operation?.state || '';
        setState(rsp.data);
        setLoadError('');
      } else setLoadError(rsp.msg);
    } catch {
      setLoadError(t('settings.software.requestFailed'));
    } finally {
      refreshInFlight.current = false;
    }
  };

  const refreshStatus = async () => {
    try {
      const rsp = await http.get('/api/os/update/software/status');
      if (rsp.code !== 0) return;
      const nextOperation = rsp.data.operation as Operation;
      const previousState = operationState.current;
      operationState.current = nextOperation.state;
      setState((current) => current ? { ...current, operation: nextOperation, indexes: rsp.data.indexes } : current);
      if (previousState === 'running' && nextOperation.state !== 'running') void refresh();
    } catch {
      // Status polling is passive. It must never interrupt the user with a toast.
    }
  };

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refreshStatus(), 3000);
    return () => window.clearInterval(timer);
  }, []);

  const run = async (action: string, name = '') => {
    setActionStarted(true);
    setBusy(true);
    try {
      const rsp = await http.post(`/api/os/update/software/${action}`, { name });
      if (rsp.code !== 0) message.error(rsp.msg);
      else message.success(t('settings.software.started'));
      await refreshStatus();
    } catch {
      message.error(t('settings.software.requestFailed'));
    } finally {
      setBusy(false);
    }
  };

  const search = async (term: string, generation: number) => {
    if (generation !== searchGeneration.current) return;
    setSearching(true);
    try {
      const rsp = await http.get('/api/os/update/software/search', { query: term });
      if (generation !== searchGeneration.current) return;
      if (rsp.code === 0) {
        setResults(rsp.data.packages || []);
        setSearchedQuery(term);
        setSearchError('');
      } else setSearchError(rsp.msg);
    } catch {
      if (generation === searchGeneration.current) setSearchError(t('settings.software.requestFailed'));
    } finally {
      if (generation === searchGeneration.current) setSearching(false);
    }
  };

  const searchNow = () => {
    const term = query.trim();
    const generation = ++searchGeneration.current;
    if (term.length < 2) {
      setResults([]);
      setSearchedQuery('');
      setSearchError('');
      setSearching(false);
      return;
    }
    void search(term, generation);
  };

  useEffect(() => {
    const term = query.trim();
    const generation = ++searchGeneration.current;
    if (term.length < 2) {
      setResults([]);
      setSearchedQuery('');
      setSearchError('');
      setSearching(false);
      return;
    }
    const timer = window.setTimeout(() => void search(term, generation), 700);
    return () => window.clearTimeout(timer);
  }, [query]);

  const previewRemoval = async (name: string) => {
    try {
      const rsp = await http.post('/api/os/update/software/remove/preview', { name });
      if (rsp.code === 0) setRemoval({ name, preview: rsp.data.preview });
      else message.error(rsp.msg);
    } catch {
      message.error(t('settings.software.requestFailed'));
    }
  };

  const loadUpdates = async () => {
    if (updatesInFlight.current) return;
    updatesInFlight.current = true;
    setUpdatesLoading(true);
    try {
      const rsp = await http.get('/api/os/update/software/updates');
      if (rsp.code === 0) {
        setUpdates(rsp.data.updates || []);
        setUpdatesError('');
      } else setUpdatesError(rsp.msg);
    } catch {
      setUpdatesError(t('settings.software.requestFailed'));
    } finally {
      updatesInFlight.current = false;
      setUpdatesLoading(false);
    }
  };

  useEffect(() => {
    if (activeTab === 'updates') void loadUpdates();
  }, [activeTab]);

  const indexWarning = state?.indexes?.state === 'missing'
    ? <Alert type="warning" showIcon message={t('settings.software.indexMissing')} />
    : state?.indexes?.state === 'stale'
      ? <Alert type="warning" showIcon message={t('settings.software.indexStale', { updated: state.indexes.updatedAt ? new Date(state.indexes.updatedAt * 1000).toLocaleString() : t('settings.software.indexUnknown') })} />
      : null;

  const tabs = [
    {
      key: 'available',
      label: t('settings.software.available'),
      children: (
        <section className="rounded-lg border border-neutral-700 p-4">
          {searchError && <Alert className="mb-3" type="error" showIcon message={searchError} />}
          <Input
            value={query}
            maxLength={128}
            onChange={(event) => setQuery(event.target.value.toLowerCase())}
            onPressEnter={searchNow}
            placeholder={t('settings.software.searchPlaceholder')}
            suffix={<Button type="text" size="small" icon={<SearchIcon size={15} />} loading={searching} onClick={searchNow} />}
          />
          <p className="mt-2 text-xs text-neutral-500">{t('settings.software.searchHint')}</p>
          <div className="mt-3 max-h-80 overflow-auto pr-3">
            {results.map((pkg) => (
              <div className="flex items-center justify-between gap-3 border-b border-neutral-800 py-2 text-sm" key={pkg.name}>
                <span className="min-w-0 truncate">{pkg.name} <span className="text-neutral-500">{pkg.version}</span></span>
                <Button size="small" type="primary" disabled={working} onClick={() => void run('install', pkg.name)}>{t('settings.software.install')}</Button>
              </div>
            ))}
            {searchedQuery === query.trim() && !searching && results.length === 0 && <p className="py-2 text-sm text-neutral-500">{t('settings.software.noResults')}</p>}
          </div>
        </section>
      )
    },
    {
      key: 'installed',
      label: t('settings.software.installed'),
      children: (
        <section className="rounded-lg border border-neutral-700 p-4">
          {loadError && <Alert className="mb-3" type="error" showIcon message={loadError} />}
          <div className="mb-3 flex items-center gap-2 font-medium">
            {t('settings.software.installed')} {!state && <Spin size="small" />}
          </div>
          <div className="max-h-96 overflow-auto pr-3">
            {state?.installed.map((pkg) => (
              <div className="flex items-center justify-between gap-3 border-b border-neutral-800 py-2 text-sm" key={pkg.name}>
                <span className="min-w-0 truncate">{pkg.name} <span className="text-neutral-500">{pkg.version}</span></span>
                <Button size="small" danger icon={<Trash2Icon size={14} />} disabled={working || protectedPackages.has(pkg.name)} onClick={() => void previewRemoval(pkg.name)}>{t('settings.software.remove')}</Button>
              </div>
            ))}
          </div>
        </section>
      )
    },
    {
      key: 'updates',
      label: t('settings.software.updatesTab'),
      children: (
        <div className="flex flex-col gap-4">
          {updatesError && <Alert type="error" showIcon message={updatesError} />}
          {showOperation && state?.operation.state === 'running' && <Alert type="info" showIcon message={t('settings.software.running', { action: state.operation.action, package: state.operation.package || '' })} />}
          {showOperation && state?.operation.state === 'failed' && <Alert type="error" showIcon message={state.operation.message || t('settings.software.failed')} />}
          {showOperation && state?.operation.state === 'succeeded' && <Alert type="success" showIcon message={state.operation.message || t('settings.software.succeeded')} />}
          <div className="rounded-lg border border-neutral-700 p-4">
            <div className="mb-3 flex items-center justify-between gap-3 font-medium">
              <span>{t('settings.software.updateAvailable')}</span>
              {updatesLoading && <Spin size="small" />}
            </div>
            <div className="max-h-80 overflow-auto pr-3">
              {updates.map((pkg) => (
                <div className="flex items-center justify-between gap-3 border-b border-neutral-800 py-2 text-sm" key={pkg.name}>
                  <span className="min-w-0 truncate">{pkg.name}</span>
                  <span className="shrink-0 text-xs text-neutral-500">{pkg.installed} → {pkg.available}</span>
                </div>
              ))}
              {!updatesLoading && updates.length === 0 && <p className="py-2 text-sm text-neutral-500">{t('settings.software.upToDate')}</p>}
            </div>
          </div>
          <Popconfirm title={t('settings.software.upgradeConfirm')} onConfirm={() => run('upgrade')} disabled={working || updates.length === 0}>
            <Button type="primary" disabled={working || updates.length === 0}>{t('settings.software.upgrade')}</Button>
          </Popconfirm>
          {showOperation && state?.operation.log && <pre className="max-h-48 overflow-auto rounded bg-neutral-900 p-3 text-xs whitespace-pre-wrap">{state.operation.log}</pre>}
        </div>
      )
    }
  ];

  return (
    <div className="flex flex-col gap-5">
      <div>
        <h2 className="text-xl">{t('settings.software.title')}</h2>
        <p className="text-sm text-neutral-400">{t('settings.software.description')}</p>
      </div>
      {indexWarning}
      <Button className="self-start" icon={<RefreshCwIcon size={15} />} disabled={working} onClick={() => run('refresh')}>
        {t('settings.software.refresh')}
      </Button>
      <Tabs className={styles.tabs} activeKey={activeTab} onChange={setActiveTab} items={tabs} />
      <Modal
        open={Boolean(removal)}
        title={t('settings.software.removeTitle', { package: removal?.name })}
        okText={t('settings.software.remove')}
        okButtonProps={{ danger: true, loading: busy }}
        onCancel={() => setRemoval(undefined)}
        onOk={() => {
          if (removal) void run('remove', removal.name);
          setRemoval(undefined);
        }}
      >
        <p>{t('settings.software.removeDescription')}</p>
        <pre className="max-h-64 overflow-auto rounded bg-neutral-900 p-3 text-xs whitespace-pre-wrap">{removal?.preview}</pre>
      </Modal>
    </div>
  );
};
