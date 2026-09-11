import { useEffect, useRef, useState } from 'react';
import { useAuth } from '@/contexts/auth';
import { Alert, Button, Collapse, InputNumber, message, Select, Switch } from 'antd';
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
  height: number;
  fps: number;
  quality: number;
  bitRate: number;
  gop: number;
};
type Draft = ScreenValues & { mode: string; codec: EncoderCodec; frameDetect: boolean };

export const VideoForm = ({
  status,
  refresh,
  setIsLocked
}: {
  status: ScreenValues & {
    monitorSupported: boolean;
    qhdSupported: boolean;
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
    height: status.height,
    fps: status.fps,
    quality: status.quality,
    bitRate: status.bitRate,
    gop: status.gop,
    mode,
    codec: getEncoderCodec(),
    frameDetect: storage.getFrameDetect()
  });
  const [draft, setDraft] = useState<Draft>(initial);
  const [saved, setSaved] = useState<Draft>(initial);
  const [busy, setBusy] = useState(false);
  const applying = useRef(false);
  const [h265Supported, setH265Supported] = useState<boolean | null>(null);
  const setResolution = useSetAtom(resolutionAtom);
  const setFps = useSetAtom(streamFpsAtom);
  const setGop = useSetAtom(streamGopAtom);
  const setQuality = useSetAtom(streamQualityAtom);
  const dirty = (Object.keys(draft) as (keyof Draft)[]).some((key) => draft[key] !== saved[key]);
  const qhdSelected =
    draft.height === 1440 ||
    (draft.height === 0 && (status.inputWidth > 1920 || status.inputHeight > 1080));
  const unstableSelected = draft.mode === 'h264' && draft.codec === 'h265' && qhdSelected;
  const directSupported = window.isSecureContext && !!window.VideoDecoder;

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
    setDraft((current) => ({ ...current, [key]: value }));
  }
  const codecValid = draft.mode === 'mjpeg' || draft.codec === 'h264' || h265Supported === true;
  const valid =
    Number.isInteger(draft.fps) &&
    draft.fps >= 10 &&
    draft.fps <= 60 &&
    Number.isInteger(draft.gop) &&
    draft.gop >= 1 &&
    draft.gop <= 100 &&
    codecValid;

  async function apply() {
    if (!dirty || !valid || applying.current) return;
    applying.current = true;
    setBusy(true);
    setIsLocked(true);
    const next = { ...draft };
    let completed = { ...saved };
    const commit = <K extends keyof Draft>(key: K) => {
      completed = { ...completed, [key]: next[key] };
      setSaved(completed);
    };
    try {
      // Persist shared settings first. A mode/codec change reloads only after
      // every request succeeds; failed requests leave the draft available to retry.
      const fields: Array<[keyof ScreenValues, string]> = [
        ['height', 'resolution'],
        ['fps', 'fps'],
        [next.mode === 'mjpeg' ? 'bitRate' : 'quality', 'quality'],
        [next.mode === 'mjpeg' ? 'quality' : 'bitRate', 'quality'],
        ['gop', 'gop'],
        ['monitor', 'monitor']
      ];
      for (const [key, type] of fields) {
        if (next[key] === completed[key]) continue;
        const rsp = await updateScreen(type, next[key]);
        if (rsp.code !== 0) throw new Error(rsp.msg || t('videoSettings.failed'));
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
      if (next.frameDetect !== completed.frameDetect) {
        const rsp = await updateFrameDetect(next.frameDetect);
        if (rsp.code !== 0) throw new Error(rsp.msg || t('videoSettings.failed'));
        storage.setFrameDetect(next.frameDetect);
        commit('frameDetect');
      }
      const reconnect = next.mode !== saved.mode || next.codec !== saved.codec;
      if (next.codec !== saved.codec) setEncoderCodec(next.codec);
      if (next.mode !== saved.mode) storage.setVideoMode(next.mode);
      setSaved(next);
      message.success(t('videoSettings.applied'));
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
      onChange={(value) => setDraft((current) => ({ ...current, [key]: value }))}
    />
  );
  return (
    <div className="space-y-6">
      <section className="space-y-3">
        <h3 className="font-medium">{t('videoSettings.monitor')}</h3>
        {row(
          t('videoSettings.monitorProfile'),
          select(
            'monitor',
            t('videoSettings.monitorProfile'),
            [
              { value: 0, label: t('videoSettings.automatic') },
              { value: 1080, label: t('videoSettings.preferFhd') },
              ...(status.qhdSupported ? [{ value: 1440, label: t('videoSettings.preferQhd') }] : [])
            ],
            !admin || !status.monitorSupported
          )
        )}
        <p className="text-xs leading-relaxed text-neutral-400">{t('videoSettings.monitorHint')}</p>
        {!status.monitorSupported && (
          <p className="text-xs text-amber-300">{t('videoSettings.monitorUnavailable')}</p>
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
            { value: 'h264', label: 'WebRTC', disabled: !window.RTCPeerConnection },
            { value: 'mjpeg', label: 'MJPEG' }
          ])
        )}
        {draft.mode !== 'mjpeg' &&
          row(
            t('screen.codec'),
            select('codec', t('screen.codec'), [
              {
                value: 'h265',
                label: `H.265 / HEVC${h265Supported === false ? ` (${t('screen.unsupported')})` : ''}`,
                disabled: h265Supported !== true
              },
              { value: 'h264', label: 'H.264 / AVC' }
            ])
          )}
        {row(
          t('videoSettings.streamResolution'),
          select(
            'height',
            t('videoSettings.streamResolution'),
            [0, ...(status.qhdSupported ? [1440] : []), 1080, 720, 600].map((value) => ({
              value,
              label: value
                ? t('videoSettings.atMost', { value: `${value}p` })
                : t('videoSettings.sameAsInput')
            })),
            !admin
          )
        )}
        {row(
          t('screen.fps'),
          <InputNumber
            className="!w-full"
            aria-label={t('screen.fps')}
            value={draft.fps}
            min={10}
            max={60}
            precision={0}
            disabled={busy || !admin}
            onChange={(value) => change('fps', value ?? 0)}
          />
        )}
        {row(
          t(draft.mode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
          select(
            draft.mode === 'mjpeg' ? 'quality' : 'bitRate',
            t(draft.mode === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
            draft.mode === 'mjpeg'
              ? [100, 80, 60, 50].map((value) => ({ value, label: `${value}%` }))
              : [10000, 5000, 3000, 2000, 1000].map((value) => ({
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
                        className="!w-full"
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
        <Button disabled={!dirty || busy} onClick={() => setDraft({ ...saved })}>
          {t('videoSettings.discard')}
        </Button>
        <span className="text-xs text-neutral-400" role="status">
          {dirty ? t('videoSettings.pending') : ''}
        </span>
      </div>
    </div>
  );
};
