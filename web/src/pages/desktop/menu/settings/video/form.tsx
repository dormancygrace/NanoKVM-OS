import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Collapse, InputNumber, message, Modal, Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import {
  BITRATE_CHOICES,
  blockingIssues,
  draftAutoBitrate,
  draftChanges,
  draftIssues,
  draftSize,
  ECONOMY_BITRATE,
  effectiveFps,
  mjpegBlocked,
  monitorTarget,
  QUALITY_CHOICES,
  recommendedResolution,
  resolutionChoices,
  resolutionOf,
  videoMemoryFor,
  withResolution,
  type BrowserSupport,
  type Reason,
  type VideoCapabilities,
  type VideoDraft
} from '@/lib/video-model';

import { Reset } from '../../screen/reset';
import { applyVideoDraft, setDeviceTransport } from './apply';
import { useSyncStreamAtoms } from './use-video-settings';

type Props = {
  caps: VideoCapabilities;
  browser: BrowserSupport;
  saved: VideoDraft;
  admin: boolean;
  refresh: () => Promise<void>;
  setIsLocked: (locked: boolean) => void;
};

type BitrateChoice = 'auto' | 'economy' | 'manual';

function bitrateChoice(draft: VideoDraft, caps: VideoCapabilities): BitrateChoice {
  if (draft.bitRate === draftAutoBitrate(draft, caps)) return 'auto';
  return draft.bitRate === ECONOMY_BITRATE ? 'economy' : 'manual';
}

/*
 * The video settings in two parts. The picture (mode, bitrate, codec) is
 * shared by every viewer and set by administrators; the transport and the
 * player are this browser's own. A mode is the monitor the computer renders
 * and the stream rate, the only choice most users need.
 */
export const VideoForm = ({ caps, browser, saved, admin, refresh, setIsLocked }: Props) => {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<VideoDraft>(saved);
  const [bitrate, setBitrate] = useState<BitrateChoice>(() => bitrateChoice(saved, caps));
  const [busy, setBusy] = useState(false);
  // The video memory mode the device is restarting with.
  const [restarting, setRestarting] = useState('');
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
      setBitrate(bitrateChoice(saved, caps));
    }
  }, [savedKey, baseline, dirty, saved, caps]);

  // Unlock once the polled capabilities report the new video memory mode.
  const activeMemory = caps.videoMemory?.active;
  useEffect(() => {
    if (!restarting || activeMemory?.toUpperCase() !== restarting) return;
    setRestarting('');
    applying.current = false;
    setBusy(false);
    setIsLocked(false);
  }, [restarting, activeMemory, setIsLocked]);

  // The automatic bitrate follows the mode and the codec.
  const auto = draftAutoBitrate(draft, caps);
  useEffect(() => {
    if (bitrate === 'auto' && draft.bitRate !== auto) setDraft((d) => ({ ...d, bitRate: auto }));
  }, [bitrate, auto, draft.bitRate]);

  const issues = draftIssues(draft, caps, browser);
  const valid = Object.keys(blockingIssues(saved, draft, caps, browser)).length === 0;
  const changes = draftChanges(saved, draft, caps);
  const modeName = (mode: string) => mode.toUpperCase();
  const reason = (r?: Reason) => (r ? t(`videoSettings.reason.${r}`) : '');
  const withReason = (label: string, r?: Reason) => (r ? `${label} — ${reason(r)}` : label);
  const hz = (value: number) => t('videoSettings.hz', { value });
  const size = (w: number, h: number) => (w && h ? `${w} × ${h}` : '');
  const mbps = (kbps: number) => t('videoSettings.mbps', { value: kbps / 1000 });

  function change<K extends keyof VideoDraft>(key: K, value: VideoDraft[K]) {
    setDraft((current) => ({ ...current, [key]: value }));
  }

  async function apply(next = draft) {
    if (applying.current) return;
    const nextChanges = draftChanges(saved, next, caps);
    if (
      nextChanges.keys.length === 0 ||
      Object.keys(blockingIssues(saved, next, caps, browser)).length
    )
      return;
    applying.current = true;
    const ask = (title: string, content: string, okText: string, cancelText?: string) =>
      new Promise<boolean>((resolve) =>
        Modal.confirm({
          title,
          content,
          okText,
          cancelText,
          onOk: () => resolve(true),
          onCancel: () => resolve(false)
        })
      );
    // The restart first: it is the larger consequence.
    const memory = admin ? nextChanges.memoryRestart : undefined;
    if (memory) {
      const mode = modeName(memory);
      const confirmed = await ask(
        t('videoSettings.memoryRestartTitle', { mode }),
        t('videoSettings.memoryRestartConfirm', { mode }),
        t('videoSettings.memoryRestartApply')
      );
      if (!confirmed) {
        applying.current = false;
        return;
      }
    }
    const powerCycle = admin && nextChanges.monitorRewrite && caps.monitor.requiresPowerCycle;
    if (powerCycle) {
      const confirmed = await ask(
        t('videoSettings.powerCycleTitle'),
        t('videoSettings.powerCycleConfirm'),
        t('videoSettings.powerCycleWrite'),
        t('videoSettings.powerCycleCancel')
      );
      if (!confirmed) {
        applying.current = false;
        return;
      }
    }
    setBusy(true);
    setIsLocked(true);
    let restart: string | undefined;
    try {
      const result = await applyVideoDraft(saved, next, caps, {
        admin,
        confirmPowerCycle: powerCycle,
        fitVideoMemory: !!memory
      });
      restart = result.restart;
      if (admin) syncAtoms(next, caps);
      if (restart !== undefined) {
        const mode = modeName(restart || memory || '');
        setRestarting(mode);
        message.info(t('screen.captureStatus.videoMemoryRebooting', { mode }));
      } else if (!result.reloading) {
        message.success(
          t(powerCycle ? 'videoSettings.powerCycleWritten' : 'videoSettings.applied')
        );
      }
    } catch (error) {
      message.error(
        error instanceof Error && error.message !== 'video-settings-failed'
          ? error.message
          : t('videoSettings.failed')
      );
    } finally {
      // The device is going down: the form stays locked until it is back.
      if (restart === undefined) {
        await refresh();
        applying.current = false;
        setBusy(false);
        setIsLocked(false);
      }
    }
  }

  const transportNames = { direct: 'Direct', webrtc: 'WebRTC', mjpeg: 'MJPEG' } as const;
  const deviceTransport = caps.stream.transport;
  // This browser plays another transport than the device's.
  const otherDeviceTransport =
    admin &&
    !!deviceTransport &&
    deviceTransport !== saved.transport &&
    draft.transport === saved.transport;

  async function makeDeviceTransport() {
    if (applying.current) return;
    applying.current = true;
    setBusy(true);
    try {
      await setDeviceTransport(saved.transport);
      message.success(t('videoSettings.applied'));
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

  const choices = resolutionChoices(caps);
  const current = resolutionOf(draft, caps);
  const recommended = recommendedResolution(caps);
  const choiceOption = (c: (typeof choices)[number]) => {
    const label = `${size(c.width, c.height)}${c.key === recommended?.key ? ` · ${t('videoSettings.modeRecommended')}` : ''}`;
    // A resolution that needs another installed video memory mode stays
    // selectable: applying it switches the mode and restarts the device.
    const memory =
      !c.available && c.reason === 'video-memory'
        ? videoMemoryFor(withResolution(draft, c, caps), caps)
        : undefined;
    return {
      value: c.key,
      label: memory
        ? `${label} — ${t('videoSettings.memoryRestartOption', { mode: modeName(memory) })}`
        : withReason(label, c.available ? undefined : c.reason),
      disabled: !c.available && !memory
    };
  };
  const landscape = choices.filter((c) => !c.portrait);
  const portrait = choices.filter((c) => c.portrait);
  const out = draftSize(draft, caps);
  const resolutionOptions = [
    ...(current
      ? []
      : [
          {
            value: 'custom',
            label: t('videoSettings.modeCustom', { value: size(out.width, out.height) }),
            disabled: true
          }
        ]),
    ...(portrait.length
      ? [
          { label: t('videoSettings.landscape'), options: landscape.map(choiceOption) },
          { label: t('videoSettings.portrait'), options: portrait.map(choiceOption) }
        ]
      : landscape.map(choiceOption))
  ];
  // The fastest rate of the resolution: the monitor cannot refresh faster.
  const maxFps = Math.min(caps.stream.maxFps, current?.rates[0] ?? caps.stream.maxFps);
  const target = monitorTarget(draft, caps);
  const delivered = effectiveFps(draft, caps);
  const mjpeg = draft.transport === 'mjpeg';

  return (
    <div className="space-y-6">
      <section className="space-y-3">
        <div>
          <h3 className="m-0 text-sm font-medium">{t('videoSettings.image')}</h3>
          <p className="text-fg-muted m-0 text-xs leading-relaxed">
            {t(admin ? 'videoSettings.imageHint' : 'videoSettings.userHint')}
          </p>
        </div>
        {row(
          t('videoSettings.resolution'),
          <Select
            className="w-full"
            aria-label={t('videoSettings.resolution')}
            value={current?.key ?? 'custom'}
            options={resolutionOptions}
            disabled={busy || !admin}
            onChange={(key: string) => {
              const choice = choices.find((c) => c.key === key);
              if (choice) setDraft((d) => withResolution(d, choice, caps));
            }}
          />,
          caps.monitor.programmable
            ? t(
                caps.monitor.requiresPowerCycle
                  ? 'videoSettings.cubeMonitorHint'
                  : 'videoSettings.modeHint'
              )
            : t('videoSettings.monitorUnavailable')
        )}
        {row(
          t('videoSettings.fpsOverride'),
          <InputNumber
            className="w-full!"
            aria-label={t('videoSettings.fpsOverride')}
            value={draft.fps || null}
            min={caps.stream.minFps}
            max={maxFps}
            precision={0}
            disabled={busy || !admin}
            onChange={(value) => change('fps', value ?? 0)}
          />,
          delivered < draft.fps
            ? t('videoSettings.fpsDelivered', {
                fps: delivered,
                size: size(out.width, out.height) || '—'
              })
            : t(
                caps.monitor.followsStreamRate
                  ? 'videoSettings.frameRateHint'
                  : 'videoSettings.frameRateFixedHint',
                {
                  value: maxFps
                }
              )
        )}
        {mjpeg
          ? row(
              t('screen.quality'),
              <Select
                className="w-full"
                aria-label={t('screen.quality')}
                value={draft.quality}
                disabled={busy || !admin}
                onChange={(value: number) => change('quality', value)}
                options={QUALITY_CHOICES.map((value) => ({ value, label: `${value}%` }))}
              />
            )
          : row(
              t('videoSettings.bitrate'),
              <Select
                className="w-full"
                aria-label={t('videoSettings.bitrate')}
                value={bitrate === 'manual' ? draft.bitRate : bitrate}
                disabled={busy || !admin}
                onChange={(value: 'auto' | 'economy' | number) => {
                  if (typeof value === 'number') {
                    setBitrate('manual');
                    change('bitRate', value);
                  } else {
                    setBitrate(value);
                    change('bitRate', value === 'auto' ? auto : ECONOMY_BITRATE);
                  }
                }}
                options={[
                  { value: 'auto', label: t('videoSettings.bitrateAuto', { value: auto / 1000 }) },
                  {
                    value: 'economy',
                    label: t('videoSettings.bitrateEconomy', { value: ECONOMY_BITRATE / 1000 })
                  },
                  ...BITRATE_CHOICES.map((value) => ({ value, label: mbps(value) }))
                ]}
              />,
              t('videoSettings.bitrateCeiling')
            )}
        {!mjpeg &&
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
                  (draft.transport === 'direct' ? browser.directH265 : browser.webrtcH265);
                return {
                  value,
                  label: withReason(
                    value === 'h265' ? 'H.265 / HEVC' : 'H.264 / AVC',
                    playable ? (value === draft.codec ? issues.codec : undefined) : 'browser'
                  ),
                  disabled: !playable
                };
              })}
            />,
            t('videoSettings.codecHint')
          )}
      </section>

      <section className="space-y-3">
        <div>
          <h3 className="m-0 text-sm font-medium">{t('videoSettings.browserSection')}</h3>
          <p className="text-fg-muted m-0 text-xs leading-relaxed">
            {t('videoSettings.browserSectionHint')}
          </p>
        </div>
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
                value !== 'mjpeg' && !(value === 'direct' ? browser.direct : browser.webrtc);
              const noMjpeg = value === 'mjpeg' && mjpegBlocked(draft, caps);
              return {
                value,
                label: withReason(
                  transportNames[value],
                  noBrowser ? 'browser' : noMjpeg ? 'mjpeg-4k' : blocked
                ),
                disabled: noBrowser || noMjpeg
              };
            })}
          />,
          admin
            ? `${t(`videoSettings.transportHint.${draft.transport}`)} ${t('videoSettings.deviceTransportHint')}`
            : t(`videoSettings.transportHint.${draft.transport}`)
        )}
        {otherDeviceTransport && (
          <Alert
            type="info"
            className="text-xs"
            title={t('videoSettings.deviceTransport', {
              transport: transportNames[deviceTransport]
            })}
            action={
              <Button
                size="small"
                type="link"
                disabled={busy}
                onClick={() => void makeDeviceTransport()}
              >
                {t('videoSettings.deviceTransportUse', {
                  transport: transportNames[saved.transport]
                })}
              </Button>
            }
          />
        )}
        {draft.transport === 'direct' &&
          row(
            t('videoSettings.player'),
            <Select
              className="w-full"
              aria-label={t('videoSettings.player')}
              value={draft.directPlayback}
              disabled={busy}
              onChange={(value) => change('directPlayback', value)}
              options={[
                { value: 'paced', label: t('videoSettings.directSmooth') },
                { value: 'immediate', label: t('videoSettings.directImmediate') }
              ]}
            />,
            t(
              draft.directPlayback === 'immediate'
                ? 'videoSettings.directImmediateHint'
                : 'videoSettings.directSmoothHint'
            )
          )}
      </section>

      <Collapse
        ghost
        items={[
          {
            key: 'advanced',
            label: t('videoSettings.advanced'),
            children: (
              <div className="space-y-3">
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
                {mjpeg &&
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
                {mjpeg &&
                  row(
                    t('screen.frameDetect'),
                    <Switch
                      aria-label={t('screen.frameDetect')}
                      checked={draft.frameDetect}
                      disabled={busy || !admin}
                      onChange={(value) => change('frameDetect', value)}
                    />,
                    t('screen.frameDetectTip')
                  )}
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

      <div className="border-line sticky bottom-0 z-10 space-y-2 border-t bg-neutral-900 py-3">
        {restarting && (
          <Alert
            type="info"
            showIcon
            message={t('screen.captureStatus.videoMemoryRebooting', { mode: restarting })}
          />
        )}
        {dirty &&
          !restarting &&
          (changes.monitorRewrite || changes.reconnect || changes.memoryRestart) && (
            <ul className="text-warning space-y-1 text-xs" role="status">
              {changes.monitorRewrite && (
                <li>
                  {t('videoSettings.changeMonitor', {
                    monitor: `${size(target.width, target.height) || t('videoSettings.automatic')}${target.refresh ? ` · ${hz(target.refresh)}` : ''}`
                  })}
                </li>
              )}
              {changes.reconnect && <li>{t('videoSettings.changeReload')}</li>}
              {changes.memoryRestart && admin && (
                <li>
                  {t('videoSettings.changeMemoryRestart', {
                    mode: modeName(changes.memoryRestart)
                  })}
                </li>
              )}
            </ul>
          )}
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="primary"
            loading={busy}
            disabled={!dirty || !valid || !!restarting}
            onClick={() => void apply()}
          >
            {t('videoSettings.apply')}
          </Button>
          <Button
            disabled={!dirty || busy}
            onClick={() => {
              setDraft(saved);
              setBitrate(bitrateChoice(saved, caps));
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
