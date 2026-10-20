import { ReactNode, useState } from 'react';
import { message } from 'antd';
import { CheckIcon, FilmIcon, GaugeIcon, MonitorIcon, RadioTowerIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  BITRATE_CHOICES,
  draftAutoBitrate,
  draftIssues,
  draftPlayable,
  ECONOMY_BITRATE,
  QUALITY_CHOICES,
  recommendedResolution,
  resolutionChoices,
  resolutionOf,
  transportChoice,
  withResolution,
  type Codec,
  type Transport,
  type VideoDraft
} from '@/lib/video-model';
import { MenuSubmenu } from '@/components/menu-item.tsx';

import { applyVideoDraft } from '../settings/video/apply';
import { useSyncStreamAtoms, useVideoSettings } from '../settings/video/use-video-settings';

type Item = {
  key: string;
  label: ReactNode;
  checked: boolean;
  disabled?: boolean;
  pick: () => void;
};

// A toolbar row that opens a submenu, like Scale: the setting and its value.
const Row = ({
  icon,
  title,
  value,
  items
}: {
  icon: ReactNode;
  title: string;
  value: string;
  items: Item[];
}) => (
  <MenuSubmenu
    title={title}
    content={
      <>
        {items.map((item) => (
          <div
            key={item.key}
            role="menuitemradio"
            aria-checked={item.checked}
            aria-disabled={item.disabled}
            className={`flex h-[30px] items-center rounded pr-5 pl-1 select-none ${
              item.disabled
                ? 'cursor-not-allowed opacity-40'
                : 'cursor-pointer hover:bg-neutral-700/70'
            }`}
            onClick={() => !item.disabled && !item.checked && item.pick()}
          >
            <div className="flex h-[14px] w-[20px] items-end text-blue-500">
              {item.checked && <CheckIcon size={14} />}
            </div>
            <span className="whitespace-nowrap">{item.label}</span>
          </div>
        ))}
      </>
    }
    popoverProps={{ placement: 'rightTop', arrow: false, align: { offset: [13, 0] } }}
  >
    <div className="flex h-[30px] cursor-pointer items-center gap-1 rounded px-3 text-neutral-300 hover:bg-neutral-700/50">
      <div className="flex h-[14px] w-[20px] items-end">{icon}</div>
      <span>{title}</span>
      <span className="ml-auto pl-4 whitespace-nowrap text-neutral-500">{value}</span>
    </div>
  </MenuSubmenu>
);

// The toolbar's video controls: what is streamed now, measured, and the mode,
// codec, bitrate and transport. Everything else is in Settings → Video.
export const QuickVideo = () => {
  const { t } = useTranslation();
  const { admin, caps, browser, saved, status, refresh } = useVideoSettings(3000);
  const syncAtoms = useSyncStreamAtoms();
  const [busy, setBusy] = useState(false);
  if (!caps || !browser || !saved || !status) return null;

  const mjpeg = saved.transport === 'mjpeg';
  const width = (mjpeg ? status.mjpegOutputWidth : status.videoOutputWidth) || status.outputWidth;
  const height =
    (mjpeg ? status.mjpegOutputHeight : status.videoOutputHeight) || status.outputHeight;
  const summary = [
    width && height ? `${width} × ${height}` : t('videoSettings.sameAsInput'),
    t('videoSettings.fpsValue', { value: status.measuredFps || 0 }),
    ...(mjpeg
      ? []
      : [
          t('videoSettings.measuredBitrate', {
            value: ((status.measuredKbps || 0) / 1000).toFixed(1)
          })
        ])
  ].join(' · ');
  const mbps = (kbps: number) => t('videoSettings.mbps', { value: kbps / 1000 });

  async function apply(draft: VideoDraft) {
    if (busy || !caps || !browser || !saved) return;
    // A choice the settings form would refuse is never applied: the page
    // would reload into a stream this browser or the monitor cannot play. A
    // user keeps the shared codec (applyVideoDraft), so that is what plays.
    const effective = admin ? draft : { ...draft, codec: saved.codec };
    if (!draftPlayable(effective, caps, browser)) {
      message.error(t('videoSettings.failed'));
      return;
    }
    setBusy(true);
    try {
      const result = await applyVideoDraft(saved, draft, caps, { admin, confirmPowerCycle: false });
      if (admin) syncAtoms(draft, caps);
      if (!result.reloading) message.success(t('videoSettings.applied'));
    } catch (error) {
      message.error(
        error instanceof Error && error.message !== 'video-settings-failed'
          ? error.message
          : t('videoSettings.failed')
      );
    } finally {
      await refresh();
      setBusy(false);
    }
  }

  // Mode: landscape modes the device offers (portrait ones are in Settings).
  // A Cube receiver applies a monitor change only after a power cycle, which
  // Settings → Video confirms first.
  const current = resolutionOf(saved, caps);
  const recommended = recommendedResolution(caps);
  const auto = draftAutoBitrate(saved, caps);
  const autoBitrate = saved.bitRate === auto;
  const codecNames: Record<Codec, string> = { h264: 'H.264', h265: 'H.265' };
  // The codec a resolution switches to, when it is not the current one.
  const modeCodec = (choice: ReturnType<typeof resolutionChoices>[number]) => {
    const codec = withResolution(saved, choice, caps).codec;
    return mjpeg || codec === saved.codec ? undefined : codec;
  };
  // A resolution applies at its fastest rate; Settings lowers it.
  const modeItems: Item[] = resolutionChoices(caps)
    .filter((c) => c.key === current?.key || (c.available && !c.portrait))
    .map((choice) => {
      const switchedCodec = choice.key === current?.key ? undefined : modeCodec(choice);
      return {
        key: choice.key,
        checked: choice.key === current?.key,
        disabled: busy || caps.monitor.requiresPowerCycle,
        label: (
          <>
            {`${choice.width} × ${choice.height} · ${t('videoSettings.fpsValue', { value: choice.key === current?.key ? saved.fps : (choice.rates[0] ?? saved.fps) })}`}
            {/* A resolution H.264 cannot encode switches to H.265, named here. */}
            {switchedCodec && (
              <span
                title={switchedCodec === 'h265' ? t('videoSettings.reason.h264-4k') : undefined}
              >
                {` · ${codecNames[switchedCodec]}`}
              </span>
            )}
            {choice.key === recommended?.key && (
              <span className="text-neutral-500"> · {t('videoSettings.modeRecommended')}</span>
            )}
          </>
        ),
        pick: () => {
          if (choice.key === current?.key) return;
          const draft = withResolution(saved, choice, caps);
          // An automatic bitrate follows the mode.
          if (autoBitrate) draft.bitRate = draftAutoBitrate(draft, caps);
          void apply(draft);
        }
      };
    });
  const modeValue = current
    ? `${current.width} × ${current.height} · ${saved.fps}`
    : `${width} × ${height} · ${saved.fps}`;

  const bitrateItems: Item[] = mjpeg
    ? QUALITY_CHOICES.map((quality) => ({
        key: `q${quality}`,
        checked: saved.quality === quality,
        disabled: busy,
        label: `${quality}%`,
        pick: () => void apply({ ...saved, quality })
      }))
    : [
        {
          key: 'auto',
          checked: autoBitrate,
          disabled: busy,
          label: t('videoSettings.bitrateAuto', { value: auto / 1000 }),
          pick: () => void apply({ ...saved, bitRate: auto })
        },
        {
          key: 'economy',
          checked: !autoBitrate && saved.bitRate === ECONOMY_BITRATE,
          disabled: busy,
          label: t('videoSettings.bitrateEconomy', { value: ECONOMY_BITRATE / 1000 }),
          pick: () => void apply({ ...saved, bitRate: ECONOMY_BITRATE })
        },
        ...BITRATE_CHOICES.map((bitRate) => ({
          key: `b${bitRate}`,
          checked: !autoBitrate && saved.bitRate !== ECONOMY_BITRATE && saved.bitRate === bitRate,
          disabled: busy,
          label: mbps(bitRate),
          pick: () => void apply({ ...saved, bitRate })
        }))
      ];
  const bitrateValue = mjpeg
    ? `${saved.quality}%`
    : autoBitrate
      ? t('videoSettings.bitrateAuto', { value: saved.bitRate / 1000 })
      : saved.bitRate === ECONOMY_BITRATE
        ? t('videoSettings.bitrateEconomy', { value: saved.bitRate / 1000 })
        : mbps(saved.bitRate);

  // Codec: either one where the current picture, transport and browser take
  // it; an unavailable one says why. An automatic bitrate follows the codec.
  const codecItems: Item[] = (['h265', 'h264'] as const).map((codec) => {
    const draft = { ...saved, codec };
    if (autoBitrate) draft.bitRate = draftAutoBitrate(draft, caps);
    const reason = codec === saved.codec ? undefined : draftIssues(draft, caps, browser).codec;
    return {
      key: codec,
      checked: saved.codec === codec,
      disabled: busy || !!reason,
      label: reason ? (
        <span title={t(`videoSettings.reason.${reason}`)}>{codecNames[codec]}</span>
      ) : (
        codecNames[codec]
      ),
      pick: () => void apply(draft)
    };
  });

  const names: Record<Transport, string> = { direct: 'Direct', webrtc: 'WebRTC', mjpeg: 'MJPEG' };
  // Each transport with a codec it plays here and in the monitor profile: the
  // current codec where it can, else the other one, named in the item.
  const transportItems: Item[] = (['direct', 'webrtc', 'mjpeg'] as const).map((transport) => {
    const next = transportChoice(saved, transport, caps, browser, admin);
    const switchesCodec = !!next && transport !== 'mjpeg' && next.codec !== saved.codec;
    return {
      key: transport,
      checked: saved.transport === transport,
      disabled: busy || !next,
      label: switchesCodec ? `${names[transport]} · ${codecNames[next.codec]}` : names[transport],
      pick: () => {
        if (next) void apply(next);
      }
    };
  });

  return (
    <div className="flex flex-col">
      <div className="px-3 pb-1 text-xs text-neutral-400">{summary}</div>
      {admin && (
        <Row
          icon={<MonitorIcon size={16} />}
          title={t('videoSettings.mode')}
          value={modeValue}
          items={modeItems}
        />
      )}
      {admin && !mjpeg && (
        <Row
          icon={<FilmIcon size={16} />}
          title={t('screen.codec')}
          value={codecNames[saved.codec]}
          items={codecItems}
        />
      )}
      {admin && (
        <Row
          icon={<GaugeIcon size={16} />}
          title={t(mjpeg ? 'screen.quality' : 'videoSettings.bitrate')}
          value={bitrateValue}
          items={bitrateItems}
        />
      )}
      <Row
        icon={<RadioTowerIcon size={16} />}
        title={t('videoSettings.transport')}
        value={names[saved.transport]}
        items={transportItems}
      />
    </div>
  );
};
