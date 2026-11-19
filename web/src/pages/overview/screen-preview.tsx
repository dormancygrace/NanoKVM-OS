import { useEffect, useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button } from 'antd';
import { useAtomValue } from 'jotai';
import { MonitorIcon, VideoIcon, VideoOffIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';

import { getScreenPreview, type ScreenPreview as Preview } from '@/api/vm.ts';
import { changeCapture } from '@/lib/capture-control.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { captureBusyAtom, captureReadyAtom, isHdmiEnabledAtom } from '@/jotai/screen.ts';
import { Panel } from '@/components/ui/settings.tsx';

// The device takes a new picture at most this often; asking sooner returns
// the same one.
const interval = 5000;
const soon = 1500;

// The host screen as a small still picture. A click opens the remote screen
// with its controls; the picture itself starts no video session.
export const ScreenPreview = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`overview.screen.${key}`);
  const { account } = useAuth();
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const ready = useAtomValue(captureReadyAtom);
  const busy = useAtomValue(captureBusyAtom);
  const [preview, setPreview] = useState<Preview>();
  const [error, setError] = useState(false);
  // the last request for a picture failed: the one shown may be out of date
  const [stale, setStale] = useState(false);

  useEffect(() => {
    if (!ready || !enabled) return;
    let active = true;
    let retry: number | undefined;
    // Only a visible page asks: a picture wakes the capture. The poll below
    // asks again when the page becomes visible.
    const read = () => {
      window.clearTimeout(retry);
      if (document.hidden) return;
      getScreenPreview()
        .then((rsp) => {
          if (!active) return;
          if (rsp.code !== 0) {
            setStale(true);
            return;
          }
          setStale(false);
          setPreview(rsp.data);
          // no picture yet (the capture was just switched on): ask again soon
          if (rsp.data.state !== 'ok') {
            retry = window.setTimeout(read, soon);
          }
        })
        .catch(() => {
          if (active) setStale(true);
        });
    };
    read();
    const stop = pollWhileVisible(read, interval);
    return () => {
      active = false;
      window.clearTimeout(retry);
      stop();
    };
  }, [ready, enabled]);

  const off = ready && !enabled;
  const picture = !off && preview?.state === 'ok' && preview.jpeg;
  const state = !ready
    ? t('capture.checking')
    : off
      ? t('capture.off')
      : !preview
        ? tr(stale ? 'unavailable' : 'loading')
        : preview.state === 'ok'
          ? ''
          : tr(preview.state === 'no-signal' ? 'noSignal' : 'unavailable');

  return (
    <Panel className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-fg text-sm font-medium">{tr('title')}</span>
        {/* A narrow window has a button of its own under the panel. */}
        {!off && (
          <Link to="/screen" className="hidden md:block">
            <Button type="primary" size="small" icon={<MonitorIcon size={14} />}>
              {tr('open')}
            </Button>
          </Link>
        )}
      </div>
      <Link
        to="/screen"
        aria-label={tr('open')}
        aria-disabled={off}
        tabIndex={off ? -1 : undefined}
        className={
          'relative flex aspect-video items-center justify-center overflow-hidden rounded-md bg-black no-underline ' +
          (off ? 'pointer-events-none' : 'group')
        }
      >
        {picture ? (
          <img
            src={`data:image/jpeg;base64,${picture}`}
            alt={tr('title')}
            className="h-full w-full object-contain transition-opacity group-hover:opacity-80"
          />
        ) : (
          <span className="flex flex-col items-center gap-2 px-4 text-center text-sm text-white/70">
            {off && <VideoOffIcon size={24} strokeWidth={1.5} aria-hidden="true" />}
            <span role="status">{state}</span>
          </span>
        )}
      </Link>
      {off && account.role === 'admin' && (
        <Button
          loading={busy}
          icon={<VideoIcon size={15} />}
          onClick={() => {
            setError(false);
            void changeCapture(true).catch(() => setError(true));
          }}
        >
          {t('capture.start')}
        </Button>
      )}
      {error && (
        <span role="alert" className="text-danger text-sm">
          {t('capture.failed')}
        </span>
      )}
      {picture && stale && (
        <span role="status" className="text-fg-muted text-xs">
          {tr('stale')}
        </span>
      )}
    </Panel>
  );
};
