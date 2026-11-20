import { useEffect, useState, type ReactNode } from 'react';
import { Switch } from 'antd';
import clsx from 'clsx';
import { useAtomValue } from 'jotai';
import { ScrollTextIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';

import { getMountedImage } from '@/api/storage.ts';
import { changeCapture } from '@/lib/capture-control.ts';
import { subscribeLEDStatus } from '@/lib/led-status.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { atxEnabledAtom } from '@/jotai/atx.ts';
import { captureBusyAtom, captureReadyAtom, isHdmiEnabledAtom } from '@/jotai/screen.ts';
import { useVirtualDisk } from '@/hooks/useVirtualDisk.ts';
import { Panel } from '@/components/ui/settings.tsx';
import { DownloadImage } from '@/pages/desktop/menu/download.tsx';
import { Image } from '@/pages/desktop/menu/image';
import { Power } from '@/pages/desktop/menu/power';
import { Terminal } from '@/pages/desktop/menu/terminal';

import { cameFrom, settingsPath, wayOf } from './paths.ts';

type Leds = { pwr: boolean; hdd: boolean } | null;

// The power and disk LEDs of the host, where ATX power control is on. leds is
// null until they are known.
function useHostLeds() {
  const atx = useAtomValue(atxEnabledAtom) === true;
  const [leds, setLeds] = useState<Leds>(null);
  useEffect(() => (atx ? subscribeLEDStatus(setLeds) : undefined), [atx]);
  return { atx, leds: atx ? leds : null };
}

const Dot = ({ on }: { on: boolean }) => (
  <span
    aria-hidden="true"
    className={clsx('size-2 shrink-0 rounded-full', on ? 'bg-success' : 'bg-neutral-500')}
  />
);

// The power LED of the host in one line, with ATX power control. It names the
// LED: one that is unlit or not wired does not mean a computer that is off.
export const HostState = () => {
  const { t } = useTranslation();
  const { leds } = useHostLeds();
  if (!leds) return null;
  return (
    <span className="text-fg-muted flex items-center gap-2 text-sm whitespace-nowrap">
      <Dot on={leds.pwr} />
      {t('settings.keyboardLedStatus.indicatorLabel', {
        label: t('settings.hostLedStatus.powerLed'),
        state: t(`settings.keyboardLedStatus.${leds.pwr ? 'on' : 'off'}`)
      })}
    </span>
  );
};

// The capture switch with its name beside it.
export const CaptureSwitch = ({ className }: { className?: string }) => {
  const { t } = useTranslation();
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const ready = useAtomValue(captureReadyAtom);
  const busy = useAtomValue(captureBusyAtom);
  const [error, setError] = useState(false);
  return (
    <label className={clsx('flex cursor-pointer items-center gap-2 text-sm', className)}>
      <span className={error ? 'text-danger' : undefined}>
        {t(error ? 'capture.failed' : 'dashboard.capture')}
      </span>
      <Switch
        size="small"
        checked={enabled}
        loading={busy}
        disabled={!ready}
        onChange={(next) => {
          setError(false);
          void changeCapture(next).catch(() => setError(true));
        }}
      />
    </label>
  );
};

const Line = ({ label, children }: { label: string; children: ReactNode }) => (
  <div className="border-line flex justify-between gap-3 border-t py-1.5 text-sm first:border-t-0">
    <dt className="text-fg-muted">{label}</dt>
    <dd className="m-0 min-w-0 text-right break-all">{children}</dd>
  </div>
);

// What the device knows about the host: its power and disk LEDs (with ATX
// power control) and the image it is given (with the virtual disk). Without
// both there is nothing to show, and no panel.
export const HostCard = ({ className }: { className?: string }) => {
  const { t } = useTranslation();
  const { atx, leds } = useHostLeds();
  const disk = useVirtualDisk() === true;
  const [image, setImage] = useState<string>();

  useEffect(() => {
    if (!disk) return;
    let active = true;
    const read = () => {
      getMountedImage()
        .then((rsp) => {
          if (active && rsp.code === 0) setImage(rsp.data?.file ?? '');
        })
        .catch(() => {});
    };
    read();
    const stop = pollWhileVisible(read, 10000);
    return () => {
      active = false;
      stop();
    };
  }, [disk]);

  if (!atx && !disk) return null;
  const led = (lit: boolean) => t(`settings.keyboardLedStatus.${lit ? 'on' : 'off'}`);

  return (
    <Panel className={clsx('flex flex-col gap-3', className)}>
      <span className="text-fg text-sm font-medium">{t('overview.host.title')}</span>
      <dl className="m-0">
        {atx && (
          <>
            <Line label={t('settings.hostLedStatus.powerLed')}>{leds ? led(leds.pwr) : '—'}</Line>
            <Line label={t('settings.hostLedStatus.hddLed')}>{leds ? led(leds.hdd) : '—'}</Line>
          </>
        )}
        {disk && (
          <Line label={t('image.title')}>
            {image === undefined
              ? '—'
              : image
                ? image.slice(image.lastIndexOf('/') + 1)
                : t('overview.host.noImage')}
          </Line>
        )}
      </dl>
    </Panel>
  );
};

const tile =
  'border-line bg-surface text-fg min-h-13 rounded-xl border px-3.5 hover:bg-neutral-700/50';

// What is done to the host without its screen, as buttons with their names:
// where there is no navigation beside the page to hold them. Each is there
// with what it needs (ATX power control, the virtual disk, an administrator).
export const HostActions = ({ admin, locked }: { admin: boolean; locked: boolean }) => {
  const { t } = useTranslation();
  const atx = useAtomValue(atxEnabledAtom) === true;
  const way = wayOf(locked);
  return (
    <div className="grid w-full grid-cols-2 gap-2">
      {atx && <Power label className={tile} placement="bottomLeft" />}
      <Image label className={tile} />
      {admin && <DownloadImage label className={tile} placement="bottomLeft" />}
      {admin && <Terminal label className={tile} placement="bottomLeft" />}
      {admin && (
        <Link
          to={settingsPath('system-events')}
          state={cameFrom('/overview')}
          {...way.props}
          className={clsx('flex items-center gap-3 text-sm no-underline', tile, way.className)}
        >
          <ScrollTextIcon size={18} />
          <span className="truncate">{t('settings.events.title')}</span>
        </Link>
      )}
    </div>
  );
};
