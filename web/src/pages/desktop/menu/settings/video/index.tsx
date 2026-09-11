import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Tag } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getScreen } from '@/api/vm';
import { getEncoderCodec } from '@/lib/encoder';
import { isHdmiEnabledAtom, videoModeAtom } from '@/jotai/screen';

import { VideoForm } from './form';

type Status = {
  width: number;
  height: number;
  quality: number;
  bitRate: number;
  gop: number;
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

export const VideoSettings = ({ setIsLocked }: { setIsLocked: (locked: boolean) => void }) => {
  const { t } = useTranslation();
  const mode = useAtomValue(videoModeAtom);
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const [status, setStatus] = useState<Status>();
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
      {status && <VideoForm status={status} refresh={refresh} setIsLocked={setIsLocked} />}
    </div>
  );
};
