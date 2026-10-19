import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, InputNumber, message, Progress, Select, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import type { MemoryLimit, MemoryStatus, MemorySwap } from '@/api/vm.ts';
import { swapRequestSize } from '@/lib/swap-request.ts';
import { themeTokens } from '@/lib/theme-tokens.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow } from '@/components/ui/settings.tsx';

const mib = (bytes: number) => `${(bytes / 1048576).toFixed(1)} MiB`;

/*
 * The Go memory limit of the NanoKVM server: near it the runtime collects
 * garbage harder instead of growing into memory that apk and the rest of the
 * system need. Without a saved limit it is a quarter of RAM. Changes apply at
 * once and persist.
 */
const ServerMemoryLimit = ({ totalBytes }: { totalBytes: number }) => {
  const { t } = useTranslation();
  const [state, setState] = useState<MemoryLimit>();
  const [value, setValue] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);

  const adopt = useCallback((next: MemoryLimit) => {
    setState(next);
    setValue(next.enabled ? next.limit : Math.max(next.default, next.minimum));
  }, []);

  useEffect(() => {
    let active = true;
    api
      .getMemoryLimit()
      .then((rsp) => {
        if (active && rsp.code === 0) adopt(rsp.data as MemoryLimit);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [adopt]);

  async function save(enabled: boolean, limit: number) {
    setBusy(true);
    try {
      const rsp = await api.setMemoryLimit(enabled, limit);
      if (rsp.code !== 0) throw new Error(rsp.msg || t('settings.memory.limitError'));
      adopt(rsp.data as MemoryLimit);
    } catch (err) {
      message.error(err instanceof Error ? err.message : t('settings.memory.limitError'));
    } finally {
      setBusy(false);
    }
  }

  if (!state) return null;
  const maximum = Math.max(state.minimum, Math.floor(totalBytes / 1048576));
  const changed = state.enabled && value !== null && value !== state.limit;
  return (
    <Panel className="space-y-4">
      <SettingRow
        label={t('settings.memory.limitTitle')}
        description={t('settings.memory.limitDescription', {
          default: state.default,
          minimum: state.minimum
        })}
        htmlFor="memory-limit"
      >
        <Switch
          id="memory-limit"
          aria-describedby="memory-limit-description"
          checked={state.enabled}
          loading={busy}
          disabled={busy}
          onChange={(enabled) => void save(enabled, value ?? state.minimum)}
        />
      </SettingRow>
      {state.enabled && (
        <SettingRow label={t('settings.memory.limitValue')} htmlFor="memory-limit-value">
          <div className="flex items-center gap-2">
            <InputNumber
              id="memory-limit-value"
              min={state.minimum}
              max={maximum}
              value={value}
              disabled={busy}
              addonAfter="MiB"
              style={{ width: 160 }}
              onChange={(next) => setValue(typeof next === 'number' ? next : null)}
              onPressEnter={() => changed && value !== null && void save(true, value)}
            />
            {changed && (
              <Button
                type="primary"
                loading={busy}
                onClick={() => value !== null && void save(true, value)}
              >
                {t('settings.memory.limitApply')}
              </Button>
            )}
          </div>
        </SettingRow>
      )}
      <p className="text-fg-muted m-0 text-sm">
        {state.current
          ? t('settings.memory.limitCurrent', { current: state.current })
          : t('settings.memory.limitNone')}
      </p>
    </Panel>
  );
};

export const Memory = () => {
  const { t } = useTranslation();
  const [data, setData] = useState<MemoryStatus>();
  const [busy, setBusy] = useState<'zram' | 'sd' | ''>('');
  const [loadError, setLoadError] = useState('');
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
    try {
      const response = await api.setMemorySwap(kind, enabled, sizeMiB, recompress);
      if (response.code !== 0) throw new Error(response.msg || t('settings.memory.changeError'));
      if (mounted.current) {
        setData(response.data);
        setLoadError('');
      }
    } catch (err) {
      if (mounted.current) {
        message.error(err instanceof Error ? err.message : t('settings.memory.changeError'));
      }
    } finally {
      mutating.current = false;
      if (mounted.current) {
        setBusy('');
        void refresh(true);
      }
    }
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
            {data.videoMemory && (
              <p className={`mt-3 mb-0 text-sm ${data.videoMemory.shared ? '' : 'text-warning'}`}>
                {t(
                  data.videoMemory.shared
                    ? 'settings.memory.videoMemoryShared'
                    : 'settings.memory.videoMemoryReserved',
                  { size: data.videoMemory.sizeMiB }
                )}
              </p>
            )}
            <p className="text-fg-muted mt-3 mb-0 text-xs">{t('settings.memory.ramNote')}</p>
          </Panel>
          {swapCard('zram', data.zram)}
          {swapCard('sd', data.sd)}
          <p className="text-fg-muted m-0 text-xs">{t('settings.memory.priorityNote')}</p>
          <ServerMemoryLimit totalBytes={data.totalBytes} />
        </>
      )}
    </div>
  );
};
