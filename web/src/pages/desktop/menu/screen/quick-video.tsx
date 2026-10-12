import { ReactNode, useState } from 'react';
import { message } from 'antd';
import { CheckIcon, GaugeIcon, MonitorIcon, RadioTowerIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import {
  BITRATE_CHOICES,
  draftAutoBitrate,
  ECONOMY_BITRATE,
  mjpegBlocked,
  modeOf,
  QUALITY_CHOICES,
  recommendedMode,
  transportPlayable,
  videoModes,
  withMode,
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
// bitrate and transport. Everything else is in Settings → Video.
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
    if (busy || !caps || !saved) return;
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
  const current = modeOf(saved, caps);
  const recommended = recommendedMode(caps);
  const auto = draftAutoBitrate(saved, caps);
  const autoBitrate = saved.bitRate === auto;
  const modeItems: Item[] = videoModes(caps)
    .filter((m) => m.key === current?.key || (m.available && !m.portrait))
    .map((mode) => ({
      key: mode.key,
      checked: mode.key === current?.key,
      disabled: busy || caps.monitor.requiresPowerCycle,
      label: (
        <>
          {`${mode.width} × ${mode.height} · ${t('videoSettings.fpsValue', { value: mode.fps })}`}
          {mode.key === recommended?.key && (
            <span className="text-neutral-500"> · {t('videoSettings.modeRecommended')}</span>
          )}
        </>
      ),
      pick: () => {
        const draft = withMode(saved, mode, caps);
        // An automatic bitrate follows the mode.
        if (autoBitrate) draft.bitRate = draftAutoBitrate(draft, caps);
        void apply(draft);
      }
    }));
  const modeValue = current
    ? `${current.width} × ${current.height} · ${current.fps}`
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

  const names: Record<Transport, string> = { direct: 'Direct', webrtc: 'WebRTC', mjpeg: 'MJPEG' };
  const transportItems: Item[] = (['direct', 'webrtc', 'mjpeg'] as const).map((transport) => {
    const next = { ...saved, transport };
    return {
      key: transport,
      checked: saved.transport === transport,
      disabled:
        busy ||
        !transportPlayable(browser, transport) ||
        (transport === 'mjpeg' && mjpegBlocked(next, caps)),
      label: names[transport],
      pick: () => void apply(next)
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
