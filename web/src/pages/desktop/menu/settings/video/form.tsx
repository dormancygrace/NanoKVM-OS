import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Collapse, InputNumber, message, Modal, Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { getMemoryStatus, reboot, setVideoMemory } from '@/api/vm';
import { pickVideoMemoryMode } from '@/lib/video-memory-mode';
import {
  BITRATE_CHOICES,
  draftAutoBitrate,
  draftChanges,
  draftIssues,
  draftSize,
  ECONOMY_BITRATE,
  effectiveFps,
  mjpegBlocked,
  modeOf,
  monitorTarget,
  QUALITY_CHOICES,
  recommendedMode,
  videoModes,
  withMode,
  type BrowserSupport,
  type Reason,
  type VideoCapabilities,
  type VideoDraft
} from '@/lib/video-model';

import { Reset } from '../../screen/reset';
import { applyVideoDraft } from './apply';
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

  // The automatic bitrate follows the mode and the codec.
  const auto = draftAutoBitrate(draft, caps);
  useEffect(() => {
    if (bitrate === 'auto' && draft.bitRate !== auto) setDraft((d) => ({ ...d, bitRate: auto }));
  }, [bitrate, auto, draft.bitRate]);

  const issues = draftIssues(draft, caps, browser);
  const valid = Object.keys(issues).length === 0;
  const changes = draftChanges(saved, draft, caps);
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
      await refresh();
      applying.current = false;
      setBusy(false);
      setIsLocked(false);
    }
  }

  // 3840x2160 needs the UHD video memory, which applies after a reboot.
  function switchToUhd() {
    Modal.confirm({
      title: t('videoSettings.uhdTitle'),
      content: t('videoSettings.uhdConfirm'),
      okText: t('videoSettings.uhdSwitch'),
      onOk: async () => {
        try {
          const status = await getMemoryStatus();
          if (status.code !== 0) throw new Error(status.msg);
          const mode = pickVideoMemoryMode(status.data.videoMemory.modes ?? [], 'uhd');
          if (!mode) throw new Error(t('videoSettings.uhdUnavailable'));
          const set = await setVideoMemory(mode);
          if (set.code !== 0) throw new Error(set.msg);
          const restart = await reboot();
          if (restart.code !== 0) throw new Error(restart.msg);
          message.info(t('videoSettings.uhdRebooting'));
        } catch (error) {
          message.error(
            error instanceof Error && error.message ? error.message : t('videoSettings.failed')
          );
          throw error;
        }
      }
    });
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

  const modes = videoModes(caps);
  const current = modeOf(draft, caps);
  const recommended = recommendedMode(caps);
  const modeLabel = (m: { width: number; height: number; fps: number }) =>
    `${size(m.width, m.height)} · ${t('videoSettings.fpsValue', { value: m.fps })}`;
  const modeOption = (m: (typeof modes)[number]) => ({
    value: m.key,
    label: withReason(
      `${modeLabel(m)}${m.key === recommended?.key ? ` · ${t('videoSettings.modeRecommended')}` : ''}`,
      m.available ? undefined : m.reason
    ),
    // A mode that only needs the UHD memory stays selectable: choosing it
    // offers the switch.
    disabled: !m.available && m.reason !== 'video-memory'
  });
  const landscape = modes.filter((m) => !m.portrait);
  const portrait = modes.filter((m) => m.portrait);
  const out = draftSize(draft, caps);
  const modeOptions = [
    ...(current
      ? []
      : [
          {
            value: 'custom',
            label: t('videoSettings.modeCustom', {
              value: modeLabel({ width: out.width, height: out.height, fps: draft.fps })
            }),
            disabled: true
          }
        ]),
    ...(portrait.length
      ? [
          { label: t('videoSettings.landscape'), options: landscape.map(modeOption) },
          { label: t('videoSettings.portrait'), options: portrait.map(modeOption) }
        ]
      : landscape.map(modeOption))
  ];
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
          t('videoSettings.mode'),
          <Select
            className="w-full"
            aria-label={t('videoSettings.mode')}
            value={current?.key ?? 'custom'}
            options={modeOptions}
            disabled={busy || !admin}
            onChange={(key: string) => {
              const mode = modes.find((m) => m.key === key);
              if (mode) setDraft((d) => withMode(d, mode, caps));
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
        {issues.monitor === 'video-memory' && target.height >= 2160 && (
          <Alert
            type="warning"
            showIcon
            message={t('videoSettings.uhdNeedsMemory')}
            description={t('videoSettings.uhdNeedsMemoryHint')}
            action={
              <Button size="small" disabled={!admin || busy} onClick={switchToUhd}>
                {t('videoSettings.uhdSwitch')}
              </Button>
            }
          />
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
                  { direct: 'Direct', webrtc: 'WebRTC', mjpeg: 'MJPEG' }[value],
                  noBrowser ? 'browser' : noMjpeg ? 'mjpeg-4k' : blocked
                ),
                disabled: noBrowser || noMjpeg
              };
            })}
          />,
          t(`videoSettings.transportHint.${draft.transport}`)
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
                {row(
                  t('videoSettings.fpsOverride'),
                  <InputNumber
                    className="w-full!"
                    aria-label={t('videoSettings.fpsOverride')}
                    value={draft.fps || null}
                    min={caps.stream.minFps}
                    max={caps.stream.maxFps}
                    precision={0}
                    disabled={busy || !admin}
                    onChange={(value) => change('fps', value ?? 0)}
                  />,
                  delivered < draft.fps
                    ? t('videoSettings.fpsDelivered', {
                        fps: delivered,
                        size: size(out.width, out.height) || '—'
                      })
                    : t('videoSettings.fpsOverrideHint')
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
