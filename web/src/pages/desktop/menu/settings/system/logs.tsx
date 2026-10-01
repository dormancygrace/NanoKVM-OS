import { useDeferredValue, useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Input, Select, Switch } from 'antd';
import { ChevronDownIcon, ChevronUpIcon, RefreshCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  getLogBoots,
  getLogs,
  type LogBoot,
  type LogBootInfo,
  type LogSnapshot,
  type LogSource
} from '@/api/logs';

const refreshInterval = 5000;

export const Logs = () => {
  const { t, i18n } = useTranslation();
  const [source, setSource] = useState<LogSource>('system');
  const [boot, setBoot] = useState<LogBoot>('current');
  const [boots, setBoots] = useState<LogBootInfo[]>([{ id: 'current', startedAt: 0, savedAt: 0 }]);
  const [archiveAvailable, setArchiveAvailable] = useState<boolean>();
  const [snapshot, setSnapshot] = useState<LogSnapshot>();
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(false);
  const [follow, setFollow] = useState(false);
  const [search, setSearch] = useState('');
  const [onlyMatches, setOnlyMatches] = useState(false);
  const [matchIndex, setMatchIndex] = useState(0);
  const [revision, setRevision] = useState(0);
  const [refreshReady, setRefreshReady] = useState(false);
  const viewer = useRef<HTMLPreElement>(null);
  const query = useDeferredValue(search).trim().toLowerCase();
  useEffect(() => {
    const controller = new AbortController();
    void getLogBoots(controller.signal)
      .then((response) => {
        if (!controller.signal.aborted && response.code === 0) {
          setBoots(response.data.boots as LogBootInfo[]);
          setArchiveAvailable(response.data.archiveAvailable as boolean);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setArchiveAvailable(false);
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    let disposed = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | undefined;
    let lastRead = 0;
    let pendingRetry = false;

    setSnapshot((current) =>
      current?.source === source && current.boot === boot ? current : undefined
    );
    setError(false);
    setRefreshReady(false);

    const schedule = (retry = false) => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (disposed) return;
        setRefreshReady(true);
        if ((retry || follow) && document.visibilityState === 'visible') void read();
      }, refreshInterval);
    };
    const read = async () => {
      if (disposed || controller || document.visibilityState !== 'visible') return;
      controller = new AbortController();
      pendingRetry = false;
      lastRead = Date.now();
      setLoading(true);
      setRefreshReady(false);
      try {
        const response = await getLogs(source, boot, controller.signal);
        if (disposed) return;
        if (response.code !== 0) throw new Error('logs unavailable');
        setSnapshot(response.data as LogSnapshot);
        if (typeof response.data.archiveAvailable === 'boolean')
          setArchiveAvailable(response.data.archiveAvailable);
        setError(false);
      } catch (cause) {
        if (!disposed && (cause as { response?: { status?: number } }).response?.status === 429) {
          pendingRetry = true;
        } else if (!disposed && !(cause instanceof Error && cause.name === 'CanceledError')) {
          setError(true);
        }
      } finally {
        controller = undefined;
        if (!disposed) {
          setLoading(pendingRetry);
          schedule(pendingRetry);
        }
      }
    };
    const visibility = () => {
      if (document.visibilityState === 'hidden') {
        clearTimeout(timer);
        controller?.abort();
      } else if (follow || lastRead === 0 || pendingRetry) {
        if (Date.now() - lastRead >= refreshInterval) void read();
        else schedule(pendingRetry);
      } else if (Date.now() - lastRead >= refreshInterval) {
        setRefreshReady(true);
      } else {
        schedule();
      }
    };

    void read();
    document.addEventListener('visibilitychange', visibility);
    return () => {
      disposed = true;
      clearTimeout(timer);
      controller?.abort();
      document.removeEventListener('visibilitychange', visibility);
    };
  }, [source, boot, follow, revision]);

  const view = useMemo(() => {
    const all = snapshot?.content ? snapshot.content.split('\n') : [];
    const found = query ? all.map((line) => line.toLowerCase().includes(query)) : [];
    const lines = onlyMatches && query ? all.filter((_, index) => found[index]) : all;
    const matches = query
      ? onlyMatches
        ? lines.map((_, index) => index)
        : all.flatMap((_, index) => (found[index] ? [index] : []))
      : [];
    return { lines, matches };
  }, [snapshot, query, onlyMatches]);

  const selectedMatch = Math.min(matchIndex, Math.max(0, view.matches.length - 1));
  const activeRow = view.matches[selectedMatch];
  const moveMatch = (direction: number) =>
    setMatchIndex(
      (value) =>
        (Math.min(value, Math.max(0, view.matches.length - 1)) + direction + view.matches.length) %
        Math.max(1, view.matches.length)
    );

  useEffect(() => {
    if (!viewer.current) return;
    if (activeRow !== undefined) {
      viewer.current.scrollTop = activeRow * 20;
    } else if (follow && boot === 'current') {
      viewer.current.scrollTop = viewer.current.scrollHeight;
    }
  }, [activeRow, snapshot, follow, boot]);

  const key = 'settings.system.logs';
  const timestamp = (at: number) => new Date(at).toLocaleTimeString(i18n.language);
  const sourceChanged = (value: LogSource) => {
    setSource(value);
    setMatchIndex(0);
  };
  const selectBoot = (value: LogBoot) => {
    setBoot(value);
    setMatchIndex(0);
    if (value !== 'current') setFollow(false);
  };

  return (
    <div className="min-w-0 space-y-4">
      <p className="mb-0 text-sm text-neutral-400">{t(`${key}.description`)}</p>
      <div className="flex flex-wrap items-center gap-3">
        <Select
          className="min-w-0 flex-1"
          style={{ minWidth: 150 }}
          aria-label={t(`${key}.source`)}
          value={source}
          onChange={sourceChanged}
          options={(['system', 'kernel', 'application'] as const).map((value) => ({
            value,
            label: t(`${key}.sources.${value}`)
          }))}
        />
        <Select
          className="min-w-0 flex-1"
          style={{ minWidth: 150 }}
          aria-label={t(`${key}.boot`)}
          value={boot}
          onChange={selectBoot}
          options={boots.map((item) => ({
            value: item.id,
            label:
              t(`${key}.boots.${item.id}`) +
              (item.id !== 'current' && item.startedAt
                ? ' · ' + new Date(item.startedAt).toLocaleString(i18n.language)
                : '')
          }))}
        />
        <Button
          icon={<RefreshCwIcon size={15} />}
          loading={loading}
          disabled={!refreshReady}
          onClick={() => setRevision((value) => value + 1)}
        >
          {t(`${key}.refresh`)}
        </Button>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          className="min-w-0 flex-1"
          style={{ minWidth: 150 }}
          aria-label={t(`${key}.search`)}
          placeholder={t(`${key}.search`)}
          value={search}
          onChange={(event) => {
            setSearch(event.target.value);
            setMatchIndex(0);
          }}
          onPressEnter={(event) => moveMatch(event.shiftKey ? -1 : 1)}
          allowClear
        />
        <span className="min-w-12 text-center text-xs text-neutral-400">
          {view.matches.length ? selectedMatch + 1 : 0} / {view.matches.length}
        </span>
        <Button
          size="small"
          aria-label={t(`${key}.previousMatch`)}
          icon={<ChevronUpIcon size={16} />}
          disabled={!view.matches.length}
          onClick={() => moveMatch(-1)}
        />
        <Button
          size="small"
          aria-label={t(`${key}.nextMatch`)}
          icon={<ChevronDownIcon size={16} />}
          disabled={!view.matches.length}
          onClick={() => moveMatch(1)}
        />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-sm">
          <Switch
            size="small"
            aria-label={t(`${key}.onlyMatches`)}
            checked={onlyMatches && !!query}
            disabled={!query}
            onChange={(value) => {
              setOnlyMatches(value);
              setMatchIndex(0);
            }}
          />
          {t(`${key}.onlyMatches`)}
        </label>
        <label className="flex items-center gap-2 text-sm">
          <Switch
            size="small"
            aria-label={t(`${key}.follow`)}
            checked={follow}
            disabled={boot !== 'current'}
            onChange={(value) => {
              setFollow(value);
            }}
          />
          {t(`${key}.follow`)}
        </label>
      </div>
      {archiveAvailable === false && (
        <Alert type="warning" showIcon title={t(`${key}.archiveUnavailable`)} />
      )}
      {archiveAvailable && boots.length === 1 && (
        <p className="mb-0 text-xs text-neutral-500">{t(`${key}.noHistory`)}</p>
      )}
      {error && <Alert type="error" showIcon title={t(`${key}.loadError`)} />}
      {snapshot?.state === 'unavailable' && (
        <Alert type="info" showIcon title={t(`${key}.unavailable`)} />
      )}
      <pre
        ref={viewer}
        tabIndex={0}
        aria-label={t(`${key}.title`)}
        aria-busy={loading}
        className="m-0 h-80 w-full max-w-full min-w-0 overflow-auto rounded-lg border border-neutral-700/60 bg-neutral-950/60 p-3 font-mono text-xs leading-5 whitespace-pre text-neutral-200 focus-visible:outline-2 focus-visible:outline-blue-400"
      >
        {view.lines.length ? (
          activeRow === undefined ? (
            view.lines.join('\n')
          ) : (
            <>
              {activeRow > 0 ? view.lines.slice(0, activeRow).join('\n') + '\n' : ''}
              <mark className="rounded bg-amber-300/25 text-amber-100">
                {view.lines[activeRow]}
              </mark>
              {activeRow < view.lines.length - 1
                ? '\n' + view.lines.slice(activeRow + 1).join('\n')
                : ''}
            </>
          )
        ) : loading && !snapshot ? (
          t(`${key}.loading`)
        ) : snapshot?.state === 'unavailable' ? (
          ''
        ) : (
          t(`${key}.empty`)
        )}
      </pre>
      {snapshot && (
        <div className="space-y-1 text-xs text-neutral-500">
          <div>
            {t(`${key}.collected`, {
              time: timestamp(snapshot.collectedAt),
              count: view.lines.length
            })}
          </div>
          {snapshot.truncated && <div>{t(`${key}.truncated`)}</div>}
        </div>
      )}
      <p className="mb-0 text-xs text-neutral-400">{t(`${key}.privacy`)}</p>
    </div>
  );
};
