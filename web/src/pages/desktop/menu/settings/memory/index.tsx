import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Progress, Select, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import type { MemoryStatus, MemorySwap } from '@/api/vm.ts';
import { swapRequestSize } from '@/lib/swap-request.ts';

const mib = (bytes: number) => `${(bytes / 1048576).toFixed(1)} MiB`;

export const Memory = () => {
  const { t } = useTranslation();
  const [data, setData] = useState<MemoryStatus>();
  const [busy, setBusy] = useState<'zram' | 'sd' | 'video' | ''>('');
  const [loadError, setLoadError] = useState('');
  const [changeError, setChangeError] = useState('');
  const error = changeError || loadError;
  const mounted = useRef(false);
  const generation = useRef(0);
  const mutating = useRef(false);
  const readInFlight = useRef<Promise<void> | null>(null);

  const refresh = useCallback(
    async function refresh(afterPending = false): Promise<void> {
      const pending = readInFlight.current;
      if (pending) {
        await pending;
        // A mutation invalidates an older read. Fetch its resulting state once
        // that read settles, while keeping every status request sequential.
        if (afterPending) await refresh();
        return;
      }
      if (!mounted.current || mutating.current) return;
      const current = ++generation.current;
      const request = (async () => {
        try {
          const response = await api.getMemoryStatus();
          if (!mounted.current || current !== generation.current) return;
          if (response.code !== 0) throw new Error(response.msg || t('settings.memory.loadError'));
          setData(response.data);
          setLoadError('');
        } catch (err) {
          if (mounted.current && current === generation.current) {
            setLoadError(err instanceof Error ? err.message : t('settings.memory.loadError'));
          }
        }
      })();
      readInFlight.current = request;
      try {
        await request;
      } finally {
        if (readInFlight.current === request) readInFlight.current = null;
      }
    },
    [t]
  );

  useEffect(() => {
    mounted.current = true;
    let disposed = false;
    let inFlight = false;
    async function poll() {
      if (disposed || inFlight || mutating.current) return;
      // Never overlap reads: a slow response must not be discarded as stale
      // by another poll, including the immediate refresh after visibility returns.
      inFlight = true;
      try {
        await refresh();
      } finally {
        inFlight = false;
      }
    }
    // Effect replay may still have an invalidated read pending.
    void refresh(true);
    const stopPolling = pollWhileVisible(() => void poll(), 3000);
    return () => {
      disposed = true;
      mounted.current = false;
      generation.current += 1;
      stopPolling();
    };
  }, [refresh]);

  async function change(
    kind: 'zram' | 'sd',
    enabled: boolean,
    sizeMiB: number,
    recompress?: boolean
  ) {
    if (mutating.current) return;
    mutating.current = true;
    generation.current++;
    setBusy(kind);
    setChangeError('');
    try {
      const response = await api.setMemorySwap(kind, enabled, sizeMiB, recompress);
      if (response.code !== 0) throw new Error(response.msg || t('settings.memory.changeError'));
      if (mounted.current) {
        setData(response.data);
        setLoadError('');
      }
    } catch (err) {
      if (mounted.current)
        setChangeError(err instanceof Error ? err.message : t('settings.memory.changeError'));
    } finally {
      mutating.current = false;
      if (mounted.current) {
        setBusy('');
        void refresh(true);
      }
    }
  }

  async function changeVideo(mode: api.VideoMemoryMode) {
    if (mutating.current) return;
    mutating.current = true;
    generation.current++;
    setBusy('video'); setChangeError('');
    try {
      const response = await api.setVideoMemory(mode);
      if (response.code !== 0) throw new Error(response.msg || t('settings.memory.changeError'));
      if (mounted.current) setData(response.data);
    } catch (err) {
      if (mounted.current) setChangeError(err instanceof Error ? err.message : t('settings.memory.changeError'));
    } finally {
      mutating.current = false;
      if (mounted.current) { setBusy(''); void refresh(true); }
    }
  }

  function swapCard(kind: 'zram' | 'sd', swap: MemorySwap) {
    // zram 0: "auto", half of the Linux memory.
    const sizes = kind === 'zram' ? [0, 32, 64, 128, 162] : [128, 256, 512];
    const half = Math.floor((data?.totalBytes ?? 0) / 2 / 1048576);
    // Auto zram is size 0 in requests; sizeMiB is then the computed size.
    const requestSize = swapRequestSize(kind, swap);
    return (
      <div className="space-y-3 rounded-lg border border-neutral-700/70 p-4">
        <div className="flex items-center justify-between gap-3">
          <label htmlFor={`memory-${kind}`} className="font-medium">
            {t(`settings.memory.${kind}Title`)}
          </label>
          <Switch
            id={`memory-${kind}`}
            checked={swap.enabled}
            loading={busy === kind}
            disabled={!!busy || !swap.available}
            onChange={(enabled) => void change(kind, enabled, requestSize)}
          />
        </div>
        <p className="text-sm text-neutral-400">{t(`settings.memory.${kind}Description`)}</p>
        {!swap.available && (
          <p className="text-sm text-amber-400">{t('settings.memory.unavailable')}</p>
        )}
        <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
          <label htmlFor={`memory-${kind}-size`}>{t('settings.memory.size')}</label>
          <Select
            id={`memory-${kind}-size`}
            value={kind === 'zram' && swap.auto ? 0 : swap.sizeMiB}
            className="w-32"
            disabled={!!busy || !swap.available}
            options={sizes.map((value) => ({
              value,
              label: value
                ? `${value} MiB`
                : t('settings.memory.zramAuto', { size: swap.auto ? swap.sizeMiB : half })
            }))}
            onChange={(size) => void change(kind, swap.enabled, size)}
          />
        </div>
        <div className="flex items-center justify-between text-sm text-neutral-400">
          <span>{t('settings.memory.used')}</span>
          <span>{mib(swap.usedBytes)}</span>
        </div>
        {kind === 'zram' && (
          <div className="space-y-1 text-sm text-neutral-400">
            <div className="flex justify-between">
              <span>{t('settings.memory.algorithm')}</span>
              <span>{swap.algorithm?.toUpperCase() || 'LZ4'}</span>
            </div>
            <div className="flex justify-between">
              <span>{t('settings.memory.actualRam')}</span>
              <span>{mib(swap.memoryBytes || 0)}</span>
            </div>
            <div className="flex items-center justify-between gap-3 pt-3">
              <label htmlFor="memory-recompress">{t('settings.memory.recompressTitle')}</label>
              <Switch
                id="memory-recompress"
                checked={!!swap.recompress}
                loading={busy === 'zram'}
                disabled={!!busy || (!swap.recompressAvailable && !swap.recompress)}
                onChange={(enabled) => void change('zram', swap.enabled, requestSize, enabled)}
              />
            </div>
            <p className="pt-1 text-xs">{t('settings.memory.recompressDescription')}</p>
            {swap.enabled && swap.recompress && !swap.recompressReady && (
              <p className="text-xs text-amber-400">{t('settings.memory.recompressNotReady')}</p>
            )}
          </div>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-5 px-1 pb-3 text-neutral-300">
      {error && <Alert type="error" message={error} showIcon />}
      {!data ? (
        <div className="py-8 text-center">
          <Spin />
        </div>
      ) : (
        <>
          <div className="rounded-lg bg-neutral-800/60 p-4">
            <div className="mb-2 flex justify-between gap-3">
              <span>{t('settings.memory.ram')}</span>
              <span>
                {mib(data.usedBytes)} / {mib(data.totalBytes)}
              </span>
            </div>
            <Progress
              percent={Math.round((data.usedBytes / data.totalBytes) * 100)}
              showInfo={false}
              strokeColor="#60a5fa"
              railColor="#404040"
            />
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
              <span className="text-neutral-400">{t('settings.memory.available')}</span>
              <span className="text-right">{mib(data.availableBytes)}</span>
              <span className="text-neutral-400">{t('settings.memory.cache')}</span>
              <span className="text-right">{mib(data.cachedBytes)}</span>
              <span className="text-neutral-400">{t('settings.memory.video')}</span>
              <span className="text-right">{mib(data.videoBytes)}</span>
            </div>
            <p className="mt-3 text-xs text-neutral-500">{t('settings.memory.ramNote')}</p>
          </div>
          {data.videoMemory && (
            <div className="space-y-3 rounded-lg border border-neutral-700/70 p-4">
              <label htmlFor="video-memory-mode" className="font-medium">{t('settings.memory.videoMode')}</label>
              <p className="text-sm text-neutral-400">{t('settings.memory.videoModeDescription')}</p>
              <Select id="video-memory-mode" className="w-full" value={data.videoMemory.selected}
                loading={busy === 'video'} disabled={!!busy || !data.videoMemory.available}
                options={(data.videoMemory.modes ?? ['cma', 'fixed']).map((mode) => ({ value: mode, label: t(`settings.memory.video_${mode}`) }))}
                onChange={(mode) => void changeVideo(mode)} />
              <p className="text-sm">{t('settings.memory.videoActive')}: {['cma', 'fixed', 'uhd'].includes(data.videoMemory.active) ? t(`settings.memory.video_${data.videoMemory.active}`) : t('settings.memory.videoUnknown')}</p>
              {!data.videoMemory.available && <p className="text-sm text-amber-400">{t('settings.memory.videoModeUnavailable')}</p>}
              {data.videoMemory.rebootRequired && <Alert type="info" showIcon message={t('settings.memory.videoReboot')} />}
            </div>
          )}
          {swapCard('zram', data.zram)}
          {swapCard('sd', data.sd)}
          <p className="text-xs text-neutral-400">{t('settings.memory.priorityNote')}</p>
        </>
      )}
    </div>
  );
};
