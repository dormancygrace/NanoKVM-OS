import { useEffect, useRef, useState } from 'react';
import { Alert, Progress, Select, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import type { MemoryStatus, MemorySwap } from '@/api/vm.ts';

import { ApplicationMemory } from './application.tsx';

const mib = (bytes: number) => `${(bytes / 1048576).toFixed(1)} MiB`;

export const Memory = () => {
  const { t } = useTranslation();
  const [data, setData] = useState<MemoryStatus>();
  const [busy, setBusy] = useState<'zram' | 'sd' | ''>('');
  const [loadError, setLoadError] = useState('');
  const [changeError, setChangeError] = useState('');
  const error = changeError || loadError;
  const mounted = useRef(false);
  const generation = useRef(0);
  const mutating = useRef(false);

  async function refresh() {
    const current = ++generation.current;
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
  }

  useEffect(() => {
    mounted.current = true;
    let disposed = false;
    let timer: number | undefined;
    async function poll() {
      if (!mutating.current) await refresh();
      // Wait for the response before scheduling another poll. Otherwise a
      // response slower than the interval is always discarded as stale.
      if (!disposed) timer = window.setTimeout(() => void poll(), 3000);
    }
    void poll();
    return () => {
      disposed = true;
      mounted.current = false;
      generation.current++;
      window.clearTimeout(timer);
    };
  }, []);

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
        void refresh();
      }
    }
  }

  function swapCard(kind: 'zram' | 'sd', swap: MemorySwap) {
    const sizes = kind === 'zram' ? [32, 64, 128, 162] : [128, 256, 512];
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
            onChange={(enabled) => void change(kind, enabled, swap.sizeMiB)}
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
            value={swap.sizeMiB}
            className="w-32"
            disabled={!!busy || !swap.available}
            options={sizes.map((value) => ({ value, label: `${value} MiB` }))}
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
                onChange={(enabled) => void change('zram', swap.enabled, swap.sizeMiB, enabled)}
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
      <h2 className="text-base font-medium">{t('settings.memory.title')}</h2>
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
              trailColor="#404040"
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
          {swapCard('zram', data.zram)}
          {swapCard('sd', data.sd)}
          <p className="text-xs text-neutral-400">{t('settings.memory.priorityNote')}</p>
          <div className="rounded-lg border border-neutral-700/70 p-2">
            <ApplicationMemory />
          </div>
        </>
      )}
    </div>
  );
};
