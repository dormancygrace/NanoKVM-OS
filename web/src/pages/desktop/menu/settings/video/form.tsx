import { useEffect, useRef, useState } from 'react';
import { useAuth } from '@/contexts/auth';
import {
  Alert,
  Button,
  Checkbox,
  Collapse,
  InputNumber,
  message,
  Modal,
  Select,
  Switch
} from 'antd';
import { useAtomValue, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { updateFrameDetect } from '@/api/stream';
import { updateScreen } from '@/api/vm';
import {
  getEncoderCodec,
  isEncoderCodecSupported,
  setEncoderCodec,
  type EncoderCodec
} from '@/lib/encoder';
import * as storage from '@/lib/localstorage';
import { isQhdStream } from '@/lib/video-policy';
import {
  resolutionAtom,
  streamFpsAtom,
  streamGopAtom,
  streamQualityAtom,
  videoModeAtom
} from '@/jotai/screen';

import { getQualityMap } from '../../screen/constants';
import { Reset } from '../../screen/reset';

type ScreenValues = {
  monitor: number;
  portrait: boolean;
  portraitResolution: number;
  height: number;
  fps: number;
  quality: number;
  bitRate: number;
  gop: number;
  gopMode: number;
};
type Draft = ScreenValues & {
  mode: string;
  codec: EncoderCodec;
  frameDetect: boolean;
  directPlayback: storage.DirectPlayback;
};

export const VideoForm = ({
  status,
  refresh,
  setIsLocked
}: {
  status: ScreenValues & {
    gopModeActive: number;
    gopModeRestartRequired: boolean;
    monitorSupported: boolean;
    monitorRequiresPowerCycle: boolean;
    monitorPowerCyclePending: boolean;
    monitorHighRefreshSupported: boolean;
    qhdSupported: boolean;
    portraitSupported: boolean;
    portraitMaxSupported: boolean;
    inputWidth: number;
    inputHeight: number;
  };
  refresh: () => Promise<void>;
  setIsLocked: (locked: boolean) => void;
}) => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const mode = useAtomValue(videoModeAtom);
  const initial = (): Draft => ({
    monitor: status.monitor,
    portrait: status.portrait === true,
    portraitResolution: [1280, 1920, 2304, 2560].includes(status.portraitResolution)
      ? status.portraitResolution
      : 1920,
    height: status.height,
    fps: status.fps,
    quality: status.quality,
    bitRate: status.bitRate,
    gop: status.gop,
    gopMode: status.gopMode,
    mode,
    codec: getEncoderCodec(),
    frameDetect: storage.getFrameDetect(),
    directPlayback: storage.getDirectPlayback()
  });
  const [draft, setDraft] = useState<Draft>(initial);
  const [saved, setSaved] = useState<Draft>(initial);
  const [customFps, setCustomFps] = useState(![120, 75, 70, 60, 50, 40, 30].includes(status.fps));
  const [busy, setBusy] = useState(false);
  const applying = useRef(false);
  const [h265Supported, setH265Supported] = useState<boolean | null>(null);
  const [directH265Supported, setDirectH265Supported] = useState<boolean | null>(null);
  const setResolution = useSetAtom(resolutionAtom);
  const setFps = useSetAtom(streamFpsAtom);
  const setGop = useSetAtom(streamGopAtom);
  const setQuality = useSetAtom(streamQualityAtom);
  const dirty = (Object.keys(draft) as (keyof Draft)[]).some((key) => draft[key] !== saved[key]);
  const qhdSelected = isQhdStream(draft.height, status.inputWidth, status.inputHeight);
  const unstableSelected = draft.mode === 'h264' && draft.codec === 'h265' && qhdSelected;
  const directSupported = window.isSecureContext && !!window.VideoDecoder;
  const maximumPortrait = draft.portrait && draft.portraitResolution === 2560;

  useEffect(() => {
    let active = true;
    void isEncoderCodecSupported('direct', 'h265').then((supported) => {
      if (active) setDirectH265Supported(supported);
    });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    let active = true;
    setH265Supported(null);
    void isEncoderCodecSupported(draft.mode === 'h264' ? 'webrtc' : 'direct', 'h265').then(
      (supported) => {
        if (active) setH265Supported(supported);
      }
    );
    return () => {
      active = false;
    };
  }, [draft.mode]);

  function change<K extends keyof Draft>(key: K, value: Draft[K]) {
    setDraft((current) => {
      const next = { ...current, [key]: value };
      if (
        (key === 'portraitResolution' || key === 'portrait') &&
        next.portrait &&
        next.portraitResolution === 2560
      ) {
        next.mode = 'direct';
        next.codec = 'h265';
        next.fps = Math.min(next.fps, 40);
      }
      if ((key === 'portraitResolution' || key === 'portrait') && next.portrait) {
        const cap = ({ 1280: 120, 1920: 70, 2304: 50, 2560: 40 } as Record<number, number>)[
          next.portraitResolution
        ];
        next.fps = Math.min(next.fps, cap);
        if (next.portraitResolution === 2304) {
          next.codec = 'h264';
          if (next.mode === 'mjpeg') next.mode = 'direct';
        }
      }
      return next;
    });
  }
  const codecValid = draft.mode === 'mjpeg' || draft.codec === 'h264' || h265Supported === true;
  const valid =
    Number.isInteger(draft.fps) &&
    draft.fps >= 10 &&
    draft.fps <= (maximumPortrait ? 40 : 120) &&
    Number.isInteger(draft.gop) &&
    draft.gop >= 1 &&
    draft.gop <= 100 &&
    codecValid &&
    (!maximumPortrait ||
      (draft.mode === 'direct' && draft.codec === 'h265' && directH265Supported === true)) &&
    !unstableSelected;

  async function apply() {
    if (!dirty || !valid || applying.current) return;
    applying.current = true;
    const powerCycleWrite = draft.monitor !== saved.monitor && status.monitorRequiresPowerCycle;
    if (powerCycleWrite) {
      const confirmed = await new Promise<boolean>((resolve) =>
        Modal.confirm({
          title: t('videoSettings.powerCycleTitle'),
          content: t('videoSettings.powerCycleConfirm'),
          okText: t('videoSettings.powerCycleWrite'),
          cancelText: t('videoSettings.powerCycleCancel'),
          onOk: () => {
            resolve(true);
          },
          onCancel: () => {
            resolve(false);
          }
        })
      );
      if (!confirmed) {
        applying.current = false;
        return;
      }
    }
    setBusy(true);
    setIsLocked(true);
    const next = { ...draft };
    let completed = { ...saved };
    let gopModeRestartRequired = status.gopModeRestartRequired;
    const commit = <K extends keyof Draft>(key: K) => {
      completed = { ...completed, [key]: next[key] };
      setSaved(completed);
    };
    try {
      // Persist shared settings first. A mode/codec change reloads only after
      // every request succeeds; failed requests leave the draft available to retry.
      const fields: Array<
        [Exclude<keyof ScreenValues, 'portrait' | 'portraitResolution'>, string]
      > = [
        ['height', 'resolution'],
        ['fps', 'fps'],
        [next.mode === 'mjpeg' ? 'bitRate' : 'quality', 'quality'],
        [next.mode === 'mjpeg' ? 'quality' : 'bitRate', 'quality'],
        ['gop', 'gop'],
        ['gopMode', 'gop_mode'],
        ['monitor', 'monitor']
      ];
      for (const [key, type] of fields) {
        if (next[key] === completed[key]) continue;
        const rsp = await updateScreen(type, next[key], type === 'monitor' && powerCycleWrite);
        if (rsp.code !== 0) throw new Error(rsp.msg || t('videoSettings.failed'));
        if (key === 'gopMode') {
          gopModeRestartRequired = rsp.data?.gopModeRestartRequired === true;
        }
        commit(key);
        if (key === 'height') {
          const resolution = {
            height: next.height,
            width: (
              { 0: 0, 600: 800, 720: 1280, 1080: 1920, 1440: 2560 } as Record<number, number>
            )[next.height]
          };
          setResolution(resolution);
          storage.setResolution(resolution);
        }
        if (key === 'fps') {
          setFps(next.fps);
          storage.setFps(next.fps);
        }
        if (key === 'gop') {
          setGop(next.gop);
          storage.setGop(next.gop);
        }
        if (key === 'quality' || key === 'bitRate') {
          const quality = [...(getQualityMap(next.mode) ?? [])].find(
            ([, value]) => value === next[key]
          )?.[0];
          if (quality !== undefined) {
            setQuality(quality);
            storage.setQuality(quality);
          }
        }
      }
      if (next.portraitResolution !== completed.portraitResolution) {
        const rsp = await updateScreen('portrait_resolution', next.portraitResolution);
        if (rsp.code !== 0) throw new Error(rsp.msg || t('videoSettings.failed'));
        commit('portraitResolution');
      }
      if (next.portrait !== completed.portrait) {
        const rsp = await updateScreen('portrait', next.portrait ? 1 : 0);
        if (rsp.code !== 0) throw new Error(rsp.msg || t('videoSettings.failed'));
        commit('portrait');
      }
      if (next.frameDetect !== completed.frameDetect) {
        const rsp = await updateFrameDetect(next.frameDetect);
        if (rsp.code !== 0) throw new Error(rsp.msg || t('videoSettings.failed'));
        storage.setFrameDetect(next.frameDetect);
        commit('frameDetect');
      }
      const playbackChanged = next.directPlayback !== saved.directPlayback;
      const gopModeChanged = next.gopMode !== saved.gopMode;
      const reconnect =
        next.mode !== saved.mode ||
        next.codec !== saved.codec ||
        (next.mode === 'direct' && playbackChanged);
      if (playbackChanged) {
        storage.setDirectPlayback(next.directPlayback);
        // An explicit UI choice replaces any diagnostic render override.
        const url = new URL(window.location.href);
        url.searchParams.delete('directRender');
        url.searchParams.delete('directBufferMs');
        window.history.replaceState(window.history.state, '', url);
      }
      if (next.codec !== saved.codec) setEncoderCodec(next.codec);
      if (next.mode !== saved.mode) storage.setVideoMode(next.mode);
      setSaved(next);
      message.success(
        t(
          gopModeChanged && gopModeRestartRequired
            ? 'videoSettings.gopModeRebootRequired'
            : powerCycleWrite
              ? 'videoSettings.powerCycleWritten'
              : 'videoSettings.applied'
        )
      );
      if (reconnect) window.location.reload();
    } catch (error) {
      message.error(error instanceof Error ? error.message : t('videoSettings.failed'));
    } finally {
      await refresh();
      applying.current = false;
      setBusy(false);
      setIsLocked(false);
    }
  }

  const row = (label: string, control: React.ReactNode) => (
    <div className="grid items-center gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(180px,1fr)]">
      <span className="text-sm text-neutral-300">{label}</span>
      {control}
    </div>
  );
  const select = (
    key: keyof Draft,
    label: string,
    options: { value: string | number; label: string; disabled?: boolean }[],
    disabled = false
  ) => (
    <Select
      className="w-full"
      aria-label={label}
      value={draft[key] as string | number}
      options={options}
      disabled={busy || disabled}
      onChange={(value) => change(key, value)}
    />
  );
  return (
    <div className="space-y-6">
      {status.monitorPowerCyclePending && (
        <Alert
          type="warning"
          showIcon
          message={t('videoSettings.powerCyclePending')}
          description={t('videoSettings.powerCyclePendingHint')}
          action={
            <Button
              disabled={busy || !admin}
              onClick={() =>
                Modal.confirm({
                  title: t('videoSettings.powerCycleAck'),
                  content: t('videoSettings.powerCycleAckConfirm'),
                  onOk: async () => {
                    const rsp = await updateScreen('monitor_power_cycle_ack', 1, true);
                    if (rsp.code !== 0) {
                      message.error(rsp.msg || t('videoSettings.failed'));
                      throw new Error(rsp.msg);
                    }
                    await refresh();
                  }
                })
              }
            >
              {t('videoSettings.powerCycleAck')}
            </Button>
          }
        />
      )}
      <section className="space-y-3">
        <h3 className="font-medium">{t('videoSettings.monitor')}</h3>
        {row(
          t('videoSettings.monitorProfile'),
          select(
            'monitor',
            t('videoSettings.monitorProfile'),
            [
              { value: 0, label: t('videoSettings.automatic') },
              ...(status.qhdSupported && status.monitorHighRefreshSupported
                ? [{ value: 1440, label: t('videoSettings.preferQhd') }]
                : []),
              {
                value: 1080,
                label: t(
                  status.monitorHighRefreshSupported
                    ? 'videoSettings.preferFhd'
                    : 'videoSettings.preferFhd60'
                )
              },
              {
                value: 720,
                label: t(
                  status.monitorHighRefreshSupported
                    ? 'videoSettings.preferHd'
                    : 'videoSettings.preferHd60'
                )
              }
            ],
            !admin || !status.monitorSupported || draft.portrait
          )
        )}
        <div className="flex items-center">
          <Checkbox
            aria-label={t('videoSettings.portrait')}
            checked={draft.portrait}
            disabled={busy || !admin || !status.portraitSupported}
            onChange={(event) => change('portrait', event.target.checked)}
          >
            {t('videoSettings.portrait')}
          </Checkbox>
        </div>
        {draft.portrait &&
          row(
            t('videoSettings.portraitProfile'),
            select(
              'portraitResolution',
              t('videoSettings.portraitProfile'),
              [
                { value: 1280, label: t('videoSettings.portraitHDProfile') },
                {
                  value: 1920,
                  label: t('videoSettings.portraitDefaultProfile')
                },
                { value: 2304, label: t('videoSettings.portraitAVCProfile') },
                ...(status.portraitMaxSupported
                  ? [
                      {
                        value: 2560,
                        label: t('videoSettings.portraitMaximumProfile'),
                        disabled: !directSupported || directH265Supported !== true
                      }
                    ]
                  : [])
              ],
              !admin || !status.portraitSupported
            )
          )}
        <p className="text-xs leading-relaxed text-neutral-400">
          {t(
            status.monitorRequiresPowerCycle
              ? 'videoSettings.cubeMonitorHint'
              : 'videoSettings.monitorHint'
          )}
        </p>
        <p className="text-xs leading-relaxed text-neutral-400">
          {t('videoSettings.portraitHint')}
        </p>
        {maximumPortrait && (
          <p className="text-xs leading-relaxed text-amber-300">
            {t('videoSettings.portraitMaximumHint')}
          </p>
        )}
        {!status.monitorSupported && (
          <p className="text-xs text-amber-300">{t('videoSettings.monitorUnavailable')}</p>
        )}
        {!status.portraitSupported && (
          <p className="text-xs text-amber-300">{t('videoSettings.portraitUnavailable')}</p>
        )}
      </section>
      <section className="space-y-3">
        <h3 className="font-medium">{t('videoSettings.stream')}</h3>
        {unstableSelected && (
          <Alert
            type="warning"
            showIcon
            message={t('videoSettings.unstableTitle')}
            description={t('videoSettings.unstableDescription')}
          />
        )}
        {row(
          t('screen.video'),
          select('mode', t('screen.video'), [
            { value: 'direct', label: 'Direct', disabled: !directSupported },
            {
              value: 'h264',
              label: 'WebRTC',
              disabled:
                maximumPortrait ||
                !window.RTCPeerConnection ||
                (draft.codec === 'h265' && qhdSelected)
            },
            { value: 'mjpeg', label: 'MJPEG', disabled: maximumPortrait }
          ])
        )}
        {draft.mode === 'direct' && (
          <>
            {row(
              t('videoSettings.directPlayback'),
              select('directPlayback', t('videoSettings.directPlayback'), [
                { value: 'paced', label: t('videoSettings.directSmooth') },
                { value: 'immediate', label: t('videoSettings.directImmediate') }
              ])
            )}
            <p className="text-xs leading-relaxed text-neutral-400">
              {t(
                draft.directPlayback === 'immediate'
                  ? 'videoSettings.directImmediateHint'
                  : 'videoSettings.directSmoothHint'
              )}{' '}
              {t('videoSettings.directPlaybackLocal')}
            </p>
          </>
        )}
        {draft.mode !== 'mjpeg' &&
          row(
            t('screen.codec'),
            select('codec', t('screen.codec'), [
              {
                value: 'h265',
                label: `H.265 / HEVC${h265Supported === false ? ` (${t('screen.unsupported')})` : ''}`,
                disabled: h265Supported !== true || (draft.mode === 'h264' && qhdSelected)
              },
              { value: 'h264', label: 'H.264 / AVC', disabled: maximumPortrait }
            ])
          )}
        {draft.mode !== 'mjpeg' &&
          draft.codec === 'h265' &&
          row(
            t('videoSettings.gopMode'),
            select(
              'gopMode',
              t('videoSettings.gopMode'),
              [
                { value: 0, label: 'NormalP' },
                { value: 1, label: 'SmartP' }
              ],
              !admin
            )
          )}
        {draft.mode !== 'mjpeg' && draft.codec === 'h265' && (
          <p className="text-xs leading-relaxed text-neutral-400">
            {t('videoSettings.gopModeHint')}
          </p>
        )}
        {row(
          t('videoSettings.streamResolution'),
          select(
            'height',
            t('videoSettings.streamResolution'),
            [0, ...(status.qhdSupported ? [1440] : []), 1080, 720, 600].map((value) => ({
              value,
              disabled:
                draft.mode === 'h264' &&
                draft.codec === 'h265' &&
                isQhdStream(value, status.inputWidth, status.inputHeight),
              label: value
                ? t('videoSettings.atMost', { value: `${value}p` })
                : t('videoSettings.sameAsInput')
            })),
            !admin
          )
        )}
        {row(
          t('screen.fps'),
          <div className="flex flex-col gap-2">
            <Select
              className="w-full"
              aria-label={t('screen.fps')}
              value={customFps ? 'custom' : draft.fps}
              options={[
                { value: 120, label: '120', disabled: maximumPortrait },
                { value: 75, label: '75', disabled: maximumPortrait },
                { value: 70, label: '70', disabled: maximumPortrait },
                { value: 60, label: '60', disabled: maximumPortrait },
                { value: 50, label: '50', disabled: maximumPortrait },
                { value: 40, label: '40', disabled: maximumPortrait },
                { value: 30, label: '30' },
                { value: 'custom', label: t('keyboard.shortcut.custom') }
              ]}
              disabled={busy || !admin}
              onChange={(value) => {
                setCustomFps(value === 'custom');
                if (value !== 'custom') change('fps', Number(value));
              }}
            />
            {customFps && (
              <InputNumber
                className="w-full!"
                aria-label={`${t('screen.fps')} — ${t('keyboard.shortcut.custom')}`}
                value={draft.fps || null}
                min={10}
                max={maximumPortrait ? 40 : 120}
                precision={0}
                disabled={busy || !admin}
                onChange={(value) => change('fps', value ?? 0)}
              />
            )}
          </div>
        )}
        {row(
          t(draft.mode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
          select(
            draft.mode === 'mjpeg' ? 'quality' : 'bitRate',
            t(draft.mode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
            draft.mode === 'mjpeg'
              ? [100, 80, 60, 50].map((value) => ({ value, label: `${value}%` }))
              : [20000, 15000, 10000, 5000, 3000, 2000, 1000].map((value) => ({
                  value,
                  label: `${value / 1000} Mbit/s`
                })),
            !admin
          )
        )}
        <p className="text-xs leading-relaxed text-neutral-400">{t('videoSettings.streamHint')}</p>
      </section>
      <Collapse
        ghost
        items={[
          {
            key: 'advanced',
            label: t('videoSettings.advanced'),
            children: (
              <div className="space-y-3">
                {draft.mode === 'mjpeg'
                  ? row(
                      t('screen.frameDetect'),
                      <Switch
                        aria-label={t('screen.frameDetect')}
                        checked={draft.frameDetect}
                        disabled={busy || !admin}
                        onChange={(value) => change('frameDetect', value)}
                      />
                    )
                  : row(
                      'GOP',
                      <InputNumber
                        className="w-full!"
                        aria-label="GOP"
                        value={draft.gop}
                        min={1}
                        max={100}
                        precision={0}
                        disabled={busy || !admin}
                        onChange={(value) => change('gop', value ?? 0)}
                      />
                    )}
                <p className="text-xs text-neutral-400">{t('videoSettings.gopHint')}</p>
                {admin && (
                  <div className={busy ? 'pointer-events-none opacity-40' : ''}>
                    <Reset />
                  </div>
                )}
              </div>
            )
          }
        ]}
      />
      <div className="sticky bottom-0 z-10 flex flex-wrap items-center gap-3 border-t border-neutral-700 bg-neutral-900 py-3">
        <Button
          type="primary"
          loading={busy}
          disabled={!dirty || !valid}
          onClick={() => void apply()}
        >
          {t('videoSettings.apply')}
        </Button>
        <Button
          disabled={!dirty || busy}
          onClick={() => {
            setDraft({ ...saved });
            setCustomFps(![120, 75, 70, 60, 50, 40, 30].includes(saved.fps));
          }}
        >
          {t('videoSettings.discard')}
        </Button>
        <span className="text-xs text-neutral-400" role="status">
          {dirty ? t('videoSettings.pending') : ''}
        </span>
      </div>
    </div>
  );
};
