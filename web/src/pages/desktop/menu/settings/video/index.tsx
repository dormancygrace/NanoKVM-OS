import { useCallback, useEffect, useState } from 'react';
import { useAuth } from '@/contexts/auth';
import { Alert, Button, Collapse, message, Select, Tag } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getScreen, updateScreen } from '@/api/vm';
import { getEncoderCodec } from '@/lib/encoder';
import { isHdmiEnabledAtom, videoModeAtom } from '@/jotai/screen';

import { Codec } from '../../screen/codec';
import { StreamControls } from '../../screen/controls';
import { FrameDetect } from '../../screen/frame-detect';
import { Reset } from '../../screen/reset';
import { VideoMode } from '../../screen/video-mode';

type Status = {
  monitor: number;
  monitorSupported: boolean;
  qhdSupported: boolean;
  inputWidth: number;
  inputHeight: number;
  outputWidth: number;
  outputHeight: number;
  fps: number;
  effectiveFps: number;
  measuredFps: number;
};

export const VideoSettings = () => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const admin = account.role === 'admin';
  const mode = useAtomValue(videoModeAtom);
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const [status, setStatus] = useState<Status>();
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const refresh = useCallback(async () => {
    try {
      const rsp = await getScreen();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setStatus(rsp.data);
      setFailed(false);
    } catch {
      setFailed(true);
    }
  }, []);
  useEffect(() => {
    void refresh();
    const timer = setInterval(() => void refresh(), 3000);
    return () => clearInterval(timer);
  }, [refresh]);
  const size = (w?: number, h?: number) => (w && h ? `${w} × ${h}` : '—');
  const transport = mode === 'h264' ? 'WebRTC' : mode === 'direct' ? 'Direct' : 'MJPEG';
  const encoding = mode === 'mjpeg' ? '' : getEncoderCodec() === 'h265' ? 'H.265' : 'H.264';
  return (
    <div className="space-y-6 pb-6">
      <div>
        <h2 className="mb-2 text-xl font-medium">{t('videoSettings.title')}</h2>
        <p className="text-sm text-neutral-400">{t('videoSettings.description')}</p>
      </div>
      {failed && (
        <Alert
          type="warning"
          showIcon
          message={t('videoSettings.statusFailed')}
          action={<Button onClick={() => void refresh()}>{t('videoSettings.retry')}</Button>}
        />
      )}
      <div
        className="rounded-xl border border-neutral-700 bg-neutral-800/50 p-4 text-sm"
        aria-live="polite"
      >
        <div className="mb-3 flex items-center justify-between">
          <span className="font-medium">{t('videoSettings.current')}</span>
          <Tag color={enabled ? 'green' : 'gold'}>
            {t(enabled ? 'videoSettings.captureOn' : 'videoSettings.captureOff')}
          </Tag>
        </div>
        <div className="flex justify-between gap-3 py-1">
          <span className="text-neutral-400">{t('videoSettings.input')}</span>
          <span>{enabled ? size(status?.inputWidth, status?.inputHeight) : '—'}</span>
        </div>
        <div className="flex justify-between gap-3 py-1">
          <span className="text-neutral-400">{t('videoSettings.output')}</span>
          <span>
            {enabled ? size(status?.outputWidth, status?.outputHeight) : '—'} · {encoding}{' '}
            {transport}
          </span>
        </div>
        <div className="flex justify-between gap-3 py-1">
          <span className="text-neutral-400">{t('videoSettings.requested')}</span>
          <span>{status?.fps ?? '—'} FPS</span>
        </div>
        <div className="flex justify-between gap-3 py-1">
          <span className="text-neutral-400">{t('videoSettings.measured')}</span>
          <span>{enabled ? (status?.measuredFps ?? '—') : 0} FPS</span>
        </div>
        {enabled && status && status.effectiveFps < status.fps && (
          <p className="mt-3 text-xs text-amber-300">
            {t('videoSettings.fpsLimited', { fps: status.effectiveFps })}
          </p>
        )}
      </div>
      <section className="space-y-3">
        <h3 className="font-medium">{t('videoSettings.monitor')}</h3>
        <Select
          className="w-full"
          aria-label={t('videoSettings.monitorProfile')}
          loading={busy}
          disabled={!admin || busy || !status?.monitorSupported}
          value={status?.monitor ?? 0}
          options={[
            { value: 0, label: t('videoSettings.automatic') },
            { value: 1080, label: t('videoSettings.preferFhd') },
            ...(status?.qhdSupported ? [{ value: 1440, label: t('videoSettings.preferQhd') }] : [])
          ]}
          onChange={async (value) => {
            setBusy(true);
            try {
              const rsp = await updateScreen('monitor', value);
              if (rsp.code !== 0) throw new Error(rsp.msg);
              await refresh();
            } catch (error) {
              message.error(error instanceof Error ? error.message : t('videoSettings.failed'));
            } finally {
              setBusy(false);
            }
          }}
        />
        <p className="text-xs leading-relaxed text-neutral-400">{t('videoSettings.monitorHint')}</p>
        {status && !status.monitorSupported && (
          <p className="text-xs text-amber-300">{t('videoSettings.monitorUnavailable')}</p>
        )}
      </section>
      <section className="space-y-3">
        <h3 className="font-medium">{t('videoSettings.stream')}</h3>
        <Alert
          type="warning"
          showIcon
          message={t('videoSettings.unstableTitle')}
          description={t('videoSettings.unstableDescription')}
        />
        <div className="rounded-lg border border-neutral-700 p-2">
          <VideoMode />
          {mode !== 'mjpeg' && <Codec />}
          <StreamControls />
        </div>
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
                {mode === 'mjpeg' ? <FrameDetect /> : <StreamControls advanced />}
                <p className="text-xs text-neutral-400">{t('videoSettings.gopHint')}</p>
                {admin && <Reset />}
              </div>
            )
          }
        ]}
      />
    </div>
  );
};
