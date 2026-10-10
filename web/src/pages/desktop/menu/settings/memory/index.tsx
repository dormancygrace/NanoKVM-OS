import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Checkbox, message, Progress, Select, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import type { MemoryStatus, MemorySwap } from '@/api/vm.ts';
import { swapRequestSize } from '@/lib/swap-request.ts';
import { themeTokens } from '@/lib/theme-tokens.ts';
import {
  canChooseFixed,
  isFixedOnly,
  parseVideoMemoryMode,
  pickVideoMemoryMode,
  videoMemoryResolutions,
  type VideoMemoryResolution
} from '@/lib/video-memory-mode.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

const mib = (bytes: number) => `${(bytes / 1048576).toFixed(1)} MiB`;

export const Memory = () => {
  const { t } = useTranslation();
  const [data, setData] = useState<MemoryStatus>();
  const [busy, setBusy] = useState<'zram' | 'sd' | 'video' | ''>('');
  const [loadError, setLoadError] = useState('');
  // The Fixed choice outlives UHD, which forces it on: UHD then QHD keeps it.
  const [fixedChoice, setFixedChoice] = useState(false);
  const mounted = useRef(false);
  const generation = useRef(0);
  const mutating = useRef(false);
  const readInFlight = useRef<Promise<void> | null>(null);
  const selectedVideoMode = data?.videoMemory?.selected;

  useEffect(() => {
    const parsed = selectedVideoMode ? parseVideoMemoryMode(selectedVideoMode) : undefined;
    if (parsed && !isFixedOnly(parsed.resolution)) setFixedChoice(parsed.fixed);
  }, [selectedVideoMode]);

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
    try {
      const response = await api.setMemorySwap(kind, enabled, sizeMiB, recompress);
      if (response.code !== 0) throw new Error(response.msg || t('settings.memory.changeError'));
      if (mounted.current) {
        setData(response.data);
        setLoadError('');
      }
    } catch (err) {
      if (mounted.current) showChangeError(err);
    } finally {
      mutating.current = false;
      if (mounted.current) {
        setBusy('');
        void refresh(true);
      }
    }
  }

  function showChangeError(err: unknown) {
    message.error(err instanceof Error ? err.message : t('settings.memory.changeError'));
  }

  async function changeVideo(mode: api.VideoMemoryMode) {
    if (mutating.current) return;
    mutating.current = true;
    generation.current++;
    setBusy('video');
    try {
      const response = await api.setVideoMemory(mode);
      if (response.code !== 0) throw new Error(response.msg || t('settings.memory.changeError'));
      if (mounted.current) setData(response.data);
    } catch (err) {
      if (mounted.current) showChangeError(err);
    } finally {
      mutating.current = false;
      if (mounted.current) {
        setBusy('');
        void refresh(true);
      }
    }
  }

  function videoLabel(mode: string) {
    const parsed = parseVideoMemoryMode(mode);
    if (!parsed) return t('settings.memory.videoUnknown');
    const label = t(`settings.memory.videoRes_${parsed.resolution}`);
    return parsed.fixed ? `${label} · ${t('settings.memory.videoFixed')}` : label;
  }

  function videoMemoryCard(video: MemoryStatus['videoMemory']) {
    const installed: readonly string[] = video.modes ?? [];
    const selected = parseVideoMemoryMode(video.selected);
    const resolution = selected?.resolution ?? 'fhd';
    const forced = isFixedOnly(resolution);
    const disabled = !!busy || !video.available;
    const select = (target: VideoMemoryResolution, fixed: boolean) => {
      const mode = pickVideoMemoryMode(installed, target, fixed);
      if (mode && mode !== video.selected) void changeVideo(mode);
    };
    return (
      <Panel className="space-y-4">
        <SettingRow
          label={t('settings.memory.videoMode')}
          description={t('settings.memory.videoModeDescription')}
          htmlFor="video-memory-mode"
          stacked
        >
          <Select
            id="video-memory-mode"
            aria-describedby="video-memory-mode-description"
            className="w-full"
            value={resolution}
            loading={busy === 'video'}
            disabled={disabled}
            options={videoMemoryResolutions.map((value) => ({
              value,
              label: t(`settings.memory.videoRes_${value}`),
              disabled: !installed.some((mode) => parseVideoMemoryMode(mode)?.resolution === value)
            }))}
            onChange={(value) => select(value, forced ? fixedChoice : !!selected?.fixed)}
          />
        </SettingRow>
        <div className="space-y-1">
          <Checkbox
            id="video-memory-fixed"
            aria-describedby="video-memory-fixed-hint"
            checked={forced || !!selected?.fixed}
            disabled={disabled || !canChooseFixed(installed, resolution)}
            onChange={(event) => {
              setFixedChoice(event.target.checked);
              select(resolution, event.target.checked);
            }}
          >
            {t('settings.memory.videoFixed')}
          </Checkbox>
          <p id="video-memory-fixed-hint" className="text-fg-muted m-0 text-xs">
            {t(forced ? 'settings.memory.videoFixedUhd' : 'settings.memory.videoFixedHint')}
          </p>
        </div>
        <p className="m-0 text-sm">
          {t('settings.memory.videoActive')}: {videoLabel(video.active)}
        </p>
        {!video.available && (
          <p className="text-warning m-0 text-sm">{t('settings.memory.videoModeUnavailable')}</p>
        )}
        {video.rebootRequired && (
          <Alert type="info" showIcon message={t('settings.memory.videoReboot')} />
        )}
      </Panel>
    );
  }

  function swapCard(kind: 'zram' | 'sd', swap: MemorySwap) {
    // zram 0: "auto", half of the Linux memory.
    const sizes = kind === 'zram' ? [0, 32, 64, 128, 162] : [128, 256, 512];
    const half = Math.floor((data?.totalBytes ?? 0) / 2 / 1048576);
    // Auto zram is size 0 in requests; sizeMiB is then the computed size.
    const requestSize = swapRequestSize(kind, swap);
    return (
      <Panel className="space-y-4">
        <SettingRow
          label={t(`settings.memory.${kind}Title`)}
          description={t(`settings.memory.${kind}Description`)}
          htmlFor={`memory-${kind}`}
        >
          <Switch
            id={`memory-${kind}`}
            aria-describedby={`memory-${kind}-description`}
            checked={swap.enabled}
            loading={busy === kind}
            disabled={!!busy || !swap.available}
            onChange={(enabled) => void change(kind, enabled, requestSize)}
          />
        </SettingRow>
        {!swap.available && (
          <p className="text-warning m-0 text-sm">{t('settings.memory.unavailable')}</p>
        )}
        <SettingRow label={t('settings.memory.size')} htmlFor={`memory-${kind}-size`}>
          <Select
            id={`memory-${kind}-size`}
            value={kind === 'zram' && swap.auto ? 0 : swap.sizeMiB}
            style={{ width: 180 }}
            disabled={!!busy || !swap.available}
            options={sizes.map((value) => ({
              value,
              label: value
                ? `${value} MiB`
                : t('settings.memory.zramAuto', { size: swap.auto ? swap.sizeMiB : half })
            }))}
            onChange={(size) => void change(kind, swap.enabled, size)}
          />
        </SettingRow>
        <div className="text-fg-muted space-y-1 text-sm">
          <div className="flex justify-between gap-3">
            <span>{t('settings.memory.used')}</span>
            <span>{mib(swap.usedBytes)}</span>
          </div>
          {kind === 'zram' && (
            <>
              <div className="flex justify-between gap-3">
                <span>{t('settings.memory.algorithm')}</span>
                <span>{swap.algorithm?.toUpperCase() || 'LZ4'}</span>
              </div>
              <div className="flex justify-between gap-3">
                <span>{t('settings.memory.actualRam')}</span>
                <span>{mib(swap.memoryBytes || 0)}</span>
              </div>
            </>
          )}
        </div>
        {kind === 'zram' && (
          <>
            <SettingRow
              label={t('settings.memory.recompressTitle')}
              description={t('settings.memory.recompressDescription')}
              htmlFor="memory-recompress"
            >
              <Switch
                id="memory-recompress"
                aria-describedby="memory-recompress-description"
                checked={!!swap.recompress}
                loading={busy === 'zram'}
                disabled={!!busy || (!swap.recompressAvailable && !swap.recompress)}
                onChange={(enabled) => void change('zram', swap.enabled, requestSize, enabled)}
              />
            </SettingRow>
            {swap.enabled && swap.recompress && !swap.recompressReady && (
              <p className="text-warning m-0 text-xs">{t('settings.memory.recompressNotReady')}</p>
            )}
          </>
        )}
      </Panel>
    );
  }

  return (
    <div className="space-y-6">
      {loadError && <Alert type="error" message={loadError} showIcon />}
      {!data ? (
        <div className="py-8 text-center">
          <Spin />
        </div>
      ) : (
        <>
          <Panel>
            <div className="mb-2 flex justify-between gap-3">
              <span>{t('settings.memory.ram')}</span>
              <span>
                {mib(data.usedBytes)} / {mib(data.totalBytes)}
              </span>
            </div>
            <Progress
              percent={Math.round((data.usedBytes / data.totalBytes) * 100)}
              showInfo={false}
              strokeColor={themeTokens.info}
              railColor="#404040"
            />
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
              <span className="text-fg-muted">{t('settings.memory.available')}</span>
              <span className="text-right">{mib(data.availableBytes)}</span>
              <span className="text-fg-muted">{t('settings.memory.cache')}</span>
              <span className="text-right">{mib(data.cachedBytes)}</span>
              <span className="text-fg-muted">{t('settings.memory.video')}</span>
              <span className="text-right">{mib(data.videoBytes)}</span>
            </div>
            <p className="text-fg-muted mt-3 mb-0 text-xs">{t('settings.memory.ramNote')}</p>
          </Panel>
          {data.videoMemory && videoMemoryCard(data.videoMemory)}
          {swapCard('zram', data.zram)}
          {swapCard('sd', data.sd)}
          <p className="text-fg-muted m-0 text-xs">{t('settings.memory.priorityNote')}</p>
        </>
      )}
    </div>
  );
};
