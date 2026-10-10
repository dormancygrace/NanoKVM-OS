import { useEffect, useRef, useState } from 'react';
import { Button, Collapse, InputNumber, message, Modal, Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import {
  BITRATE_CHOICES,
  draftChanges,
  draftIssues,
  effectiveFps,
  FPS_CHOICES,
  matchPreset,
  mjpegBlocked,
  monitorTarget,
  QUALITY_CHOICES,
  recommendedBitrate,
  streamSize,
  type BrowserSupport,
  type Reason,
  type VideoCapabilities,
  type VideoDraft
} from '@/lib/video-model';

import { Reset } from '../../screen/reset';
import { applyVideoDraft } from './apply';
import { PresetPicker } from './presets';
import { useSyncStreamAtoms } from './use-video-settings';

type Props = {
  caps: VideoCapabilities;
  browser: BrowserSupport;
  saved: VideoDraft;
  admin: boolean;
  refresh: () => Promise<void>;
  setIsLocked: (locked: boolean) => void;
};

export const VideoForm = ({ caps, browser, saved, admin, refresh, setIsLocked }: Props) => {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<VideoDraft>(saved);
  const [busy, setBusy] = useState(false);
  const [customFps, setCustomFps] = useState(!FPS_CHOICES.includes(saved.fps));
  const applying = useRef(false);
  const syncAtoms = useSyncStreamAtoms();

  // Follow device changes made elsewhere while nothing is being edited.
  const savedKey = JSON.stringify(saved);
  const [baseline, setBaseline] = useState(savedKey);
  const dirty = JSON.stringify(draft) !== savedKey;
  useEffect(() => {
    if (savedKey === baseline) return;
    setBaseline(savedKey);
    if (!dirty || applying.current) {
      setDraft(saved);
      setCustomFps(!FPS_CHOICES.includes(saved.fps));
    }
  }, [savedKey, baseline, dirty, saved]);

  const issues = draftIssues(draft, caps, browser);
  const valid = Object.keys(issues).length === 0;
  const changes = draftChanges(saved, draft, caps);
  const preset = matchPreset(draft, caps, browser);
  const reason = (r?: Reason) => (r ? t(`videoSettings.reason.${r}`) : '');
  const withReason = (label: string, r?: Reason) => (r ? `${label} — ${reason(r)}` : label);
  const hz = (value: number) => t('videoSettings.hz', { value });
  const size = (w: number, h: number) => (w && h ? `${w} × ${h}` : '');

  function change<K extends keyof VideoDraft>(key: K, value: VideoDraft[K]) {
    setDraft((current) => {
      const next = { ...current, [key]: value };
      // The tallest portrait profile only plays as H.265 Direct.
      const profile = caps.monitor.portrait.profiles.find((p) => p.resolution === next.portrait);
      if ((key === 'portrait' || key === 'monitor') && profile) {
        if (!profile.transports.includes(next.transport)) next.transport = 'direct';
        if (next.transport !== 'mjpeg' && !profile.codecs.includes(next.codec)) {
          next.codec = profile.codecs.includes('h265') ? 'h265' : 'h264';
        }
        next.fps = Math.min(next.fps, profile.rate);
      }
      return next;
    });
  }

  async function apply(next = draft) {
    if (applying.current) return;
    const nextChanges = draftChanges(saved, next, caps);
    if (nextChanges.keys.length === 0 || Object.keys(draftIssues(next, caps, browser)).length)
      return;
    applying.current = true;
    const powerCycle = admin && nextChanges.monitorRewrite && caps.monitor.requiresPowerCycle;
    if (powerCycle) {
      const confirmed = await new Promise<boolean>((resolve) =>
        Modal.confirm({
          title: t('videoSettings.powerCycleTitle'),
          content: t('videoSettings.powerCycleConfirm'),
          okText: t('videoSettings.powerCycleWrite'),
          cancelText: t('videoSettings.powerCycleCancel'),
          onOk: () => resolve(true),
          onCancel: () => resolve(false)
        })
      );
      if (!confirmed) {
        applying.current = false;
        return;
      }
    }
    setBusy(true);
    setIsLocked(true);
    try {
      const result = await applyVideoDraft(saved, next, caps, {
        admin,
        confirmPowerCycle: powerCycle
      });
      if (admin) syncAtoms(next, caps);
      if (!result.reloading) {
        message.success(
          t(
            result.gopModeRestartRequired
              ? 'videoSettings.gopModeRebootRequired'
              : powerCycle
                ? 'videoSettings.powerCycleWritten'
                : 'videoSettings.applied'
          )
        );
      }
    } catch (error) {
      message.error(
        error instanceof Error && error.message !== 'video-settings-failed'
          ? error.message
          : t('videoSettings.failed')
      );
    } finally {
      await refresh();
      applying.current = false;
      setBusy(false);
      setIsLocked(false);
    }
  }

  const row = (label: string, control: React.ReactNode, note?: React.ReactNode) => (
    <div className="grid items-start gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(200px,1.2fr)]">
      <span className="text-fg pt-1 text-sm">{label}</span>
      <div className="flex flex-col gap-1">
        {control}
        {note && <span className="text-fg-muted text-xs leading-relaxed">{note}</span>}
      </div>
    </div>
  );

  // The monitor and portrait profiles are one choice: what the source sees.
  const monitorValue = draft.portrait ? `p${draft.portrait}` : `m${draft.monitor}`;
  const monitorOptions = [
    {
      label: t('videoSettings.landscape'),
      options: caps.monitor.modes.map((mode) => {
        const top = mode.rates[0];
        const name = mode.height
          ? `${size(mode.width, mode.height)}${top ? ` · ${t('videoSettings.upToHz', { value: top })}` : ''}`
          : t('videoSettings.automatic');
        return {
          value: `m${mode.height}`,
          label: withReason(name, mode.available ? undefined : mode.reason),
          disabled: !mode.available
        };
      })
    },
    ...(caps.monitor.portrait.profiles.length
      ? [
          {
            label: t('videoSettings.portrait'),
            options: caps.monitor.portrait.profiles.map((p) => {
              const only =
                p.codecs.length === 1
                  ? ` · ${p.codecs[0] === 'h265' ? 'H.265' : 'H.264'}${p.transports.length === 1 ? ' Direct' : ''}`
                  : '';
              return {
                value: `p${p.resolution}`,
                label: withReason(
                  `${size(p.width, p.resolution)} · ${hz(p.rate)}${only}`,
                  p.available ? undefined : p.reason
                ),
                disabled: !p.available
              };
            })
          }
        ]
      : [])
  ];
  const target = monitorTarget(draft, caps);
  const out = streamSize(draft, caps);
  const delivered = effectiveFps(draft, caps);
  const monitorEditable = admin && caps.monitor.programmable;

  return (
    <div className="space-y-6">
      {admin && (
        <PresetPicker
          caps={caps}
          browser={browser}
          current={draft}
          selected={preset}
          disabled={busy}
          onPick={(next) => {
            setDraft(next);
            setCustomFps(!FPS_CHOICES.includes(next.fps));
          }}
        />
      )}
      {!admin && (
        <p className="text-fg-muted text-xs leading-relaxed">{t('videoSettings.userHint')}</p>
      )}

      <Collapse
        ghost
        defaultActiveKey={preset === 'custom' || !admin ? ['manual'] : []}
        items={[
          {
            key: 'manual',
            label: t('videoSettings.manual'),
            children: (
              <div className="space-y-6">
                <section className="space-y-3">
                  <h3 className="mt-0 text-sm font-medium">{t('videoSettings.monitor')}</h3>
                  {row(
                    t('videoSettings.monitorProfile'),
                    <Select
                      className="w-full"
                      aria-label={t('videoSettings.monitorProfile')}
                      value={monitorValue}
                      options={monitorOptions}
                      disabled={busy || !monitorEditable}
                      onChange={(value: string) => {
                        const n = Number(value.slice(1));
                        if (value[0] === 'p') change('portrait', n);
                        else {
                          setDraft((d) => ({ ...d, portrait: 0 }));
                          change('monitor', n);
                        }
                      }}
                    />,
                    caps.monitor.programmable
                      ? target.refresh
                        ? caps.monitor.followsStreamRate && !draft.portrait
                          ? t('videoSettings.refreshFollows', { value: target.refresh })
                          : hz(target.refresh)
                        : undefined
                      : t('videoSettings.monitorUnavailable')
                  )}
                  <p className="text-fg-muted text-xs leading-relaxed">
                    {t(
                      caps.monitor.requiresPowerCycle
                        ? 'videoSettings.cubeMonitorHint'
                        : 'videoSettings.monitorHint'
                    )}
                  </p>
                </section>

                <section className="space-y-3">
                  <h3 className="mt-0 text-sm font-medium">{t('videoSettings.stream')}</h3>
                  {row(
                    t('videoSettings.transport'),
                    <Select
                      className="w-full"
                      aria-label={t('screen.video')}
                      value={draft.transport}
                      disabled={busy}
                      onChange={(value) => change('transport', value)}
                      options={(['direct', 'webrtc', 'mjpeg'] as const).map((value) => {
                        const blocked = value === draft.transport ? issues.transport : undefined;
                        const noBrowser =
                          value !== 'mjpeg' &&
                          !(value === 'direct' ? browser.direct : browser.webrtc);
                        const noMjpeg = value === 'mjpeg' && mjpegBlocked(draft, caps);
                        return {
                          value,
                          label: withReason(
                            { direct: 'Direct', webrtc: 'WebRTC', mjpeg: 'MJPEG' }[value],
                            noBrowser ? 'browser' : noMjpeg ? 'mjpeg-4k' : blocked
                          ),
                          disabled: noBrowser || noMjpeg
                        };
                      })}
                    />,
                    t(`videoSettings.transportHint.${draft.transport}`)
                  )}
                  {draft.transport !== 'mjpeg' &&
                    row(
                      t('screen.codec'),
                      <Select
                        className="w-full"
                        aria-label={t('screen.codec')}
                        value={draft.codec}
                        disabled={busy || !admin}
                        onChange={(value) => change('codec', value)}
                        options={(['h265', 'h264'] as const).map((value) => {
                          const playable =
                            value === 'h264' ||
                            (draft.transport === 'direct'
                              ? browser.directH265
                              : browser.webrtcH265);
                          return {
                            value,
                            label: withReason(
                              value === 'h265' ? 'H.265 / HEVC' : 'H.264 / AVC',
                              playable
                                ? value === draft.codec
                                  ? issues.codec
                                  : undefined
                                : 'browser'
                            ),
                            disabled: !playable
                          };
                        })}
                      />
                    )}
                  {row(
                    t('videoSettings.streamResolution'),
                    <Select
                      className="w-full"
                      aria-label={t('videoSettings.streamResolution')}
                      value={draft.height}
                      disabled={busy || !admin}
                      onChange={(value) => change('height', value)}
                      options={caps.stream.limits.map((limit) => ({
                        value: limit.height,
                        label: withReason(
                          limit.height
                            ? t('videoSettings.atMost', { value: `${limit.height}p` })
                            : t('videoSettings.sameAsInput'),
                          limit.available ? undefined : limit.reason
                        ),
                        disabled: !limit.available
                      }))}
                    />,
                    out.width
                      ? t('videoSettings.streamSize', { value: size(out.width, out.height) })
                      : undefined
                  )}
                  {row(
                    t('screen.fps'),
                    <div className="flex flex-col gap-2">
                      <Select
                        className="w-full"
                        aria-label={t('screen.fps')}
                        value={customFps ? 'custom' : draft.fps}
                        disabled={busy || !admin}
                        options={[
                          ...FPS_CHOICES.map((value) => ({ value, label: `${value}` })),
                          { value: 'custom', label: t('keyboard.shortcut.custom') }
                        ]}
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
                          min={caps.stream.minFps}
                          max={caps.stream.maxFps}
                          precision={0}
                          disabled={busy || !admin}
                          onChange={(value) => change('fps', value ?? 0)}
                        />
                      )}
                    </div>,
                    delivered < draft.fps
                      ? t('videoSettings.fpsDelivered', {
                          fps: delivered,
                          size: size(out.width, out.height) || '—'
                        })
                      : undefined
                  )}
                  {row(
                    t(draft.transport === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'),
                    <Select
                      className="w-full"
                      aria-label={t(
                        draft.transport === 'mjpeg' ? 'screen.quality' : 'videoSettings.bitrate'
                      )}
                      value={draft.transport === 'mjpeg' ? draft.quality : draft.bitRate}
                      disabled={busy || !admin}
                      onChange={(value: number) =>
                        change(draft.transport === 'mjpeg' ? 'quality' : 'bitRate', value)
                      }
                      options={
                        draft.transport === 'mjpeg'
                          ? QUALITY_CHOICES.map((value) => ({ value, label: `${value}%` }))
                          : BITRATE_CHOICES.map((value) => ({
                              value,
                              label: t('videoSettings.mbps', { value: value / 1000 })
                            }))
                      }
                    />,
                    draft.transport !== 'mjpeg' && out.width
                      ? t('videoSettings.bitrateHint', {
                          value:
                            recommendedBitrate(out.width, out.height, delivered, draft.codec) / 1000
                        })
                      : undefined
                  )}
                  <p className="text-fg-muted text-xs leading-relaxed">
                    {t('videoSettings.streamHint')}
                  </p>
                </section>

                <section className="space-y-3">
                  <h3 className="mt-0 text-sm font-medium">{t('videoSettings.advanced')}</h3>
                  {draft.transport === 'direct' &&
                    row(
                      t('videoSettings.directPlayback'),
                      <Select
                        className="w-full"
                        aria-label={t('videoSettings.directPlayback')}
                        value={draft.directPlayback}
                        disabled={busy}
                        onChange={(value) => change('directPlayback', value)}
                        options={[
                          { value: 'paced', label: t('videoSettings.directSmooth') },
                          { value: 'immediate', label: t('videoSettings.directImmediate') }
                        ]}
                      />,
                      `${t(
                        draft.directPlayback === 'immediate'
                          ? 'videoSettings.directImmediateHint'
                          : 'videoSettings.directSmoothHint'
                      )} ${t('videoSettings.directPlaybackLocal')}`
                    )}
                  {draft.transport !== 'mjpeg' &&
                    row(
                      'GOP',
                      <InputNumber
                        className="w-full!"
                        aria-label="GOP"
                        value={draft.gop}
                        min={1}
                        max={100}
                        precision={0}
                        disabled={busy || !admin}
                        onChange={(value) => change('gop', value ?? 1)}
                      />,
                      t('videoSettings.gopHint')
                    )}
                  {draft.transport !== 'mjpeg' &&
                    draft.codec === 'h265' &&
                    row(
                      t('videoSettings.gopMode'),
                      <Select
                        className="w-full"
                        aria-label={t('videoSettings.gopMode')}
                        value={draft.gopMode}
                        disabled={busy || !admin}
                        onChange={(value) => change('gopMode', value)}
                        options={[
                          { value: 0, label: 'NormalP' },
                          { value: 1, label: 'SmartP' }
                        ]}
                      />,
                      t('videoSettings.gopModeHint')
                    )}
                  {draft.transport === 'mjpeg' &&
                    row(
                      t('videoSettings.mjpegChroma'),
                      <Select
                        className="w-full"
                        aria-label={t('videoSettings.mjpegChroma')}
                        value={draft.mjpegChroma}
                        disabled={busy || !admin}
                        onChange={(value) => change('mjpegChroma', value)}
                        options={[
                          { value: 420, label: t('videoSettings.mjpegChroma420') },
                          { value: 422, label: t('videoSettings.mjpegChroma422') }
                        ]}
                      />,
                      t('videoSettings.mjpegChromaHint')
                    )}
                  {draft.transport === 'mjpeg' &&
                    row(
                      t('screen.frameDetect'),
                      <Switch
                        aria-label={t('screen.frameDetect')}
                        checked={draft.frameDetect}
                        disabled={busy || !admin}
                        onChange={(value) => change('frameDetect', value)}
                      />
                    )}
                  {admin && (
                    <div className={busy ? 'pointer-events-none opacity-40' : ''}>
                      <Reset />
                    </div>
                  )}
                </section>
              </div>
            )
          }
        ]}
      />

      <div className="border-line sticky bottom-0 z-10 space-y-2 border-t bg-neutral-900 py-3">
        {dirty && (changes.monitorRewrite || changes.reconnect) && (
          <ul className="text-warning space-y-1 text-xs" role="status">
            {changes.monitorRewrite && (
              <li>
                {t('videoSettings.changeMonitor', {
                  monitor: `${size(target.width, target.height) || t('videoSettings.automatic')}${target.refresh ? ` · ${hz(target.refresh)}` : ''}`
                })}
              </li>
            )}
            {changes.reconnect && <li>{t('videoSettings.changeReload')}</li>}
          </ul>
        )}
        <div className="flex flex-wrap items-center gap-3">
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
              setDraft(saved);
              setCustomFps(!FPS_CHOICES.includes(saved.fps));
            }}
          >
            {t('videoSettings.discard')}
          </Button>
          <span className="text-fg-muted text-xs" role="status">
            {dirty ? (valid ? t('videoSettings.pending') : t('videoSettings.invalid')) : ''}
          </span>
        </div>
      </div>
    </div>
  );
};
