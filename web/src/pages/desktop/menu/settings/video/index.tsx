import { useState } from 'react';
import { Alert, Button, message, Modal } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import { updateScreen } from '@/api/vm';
import { monitorTarget, nominalRate } from '@/lib/video-model';
import { isHdmiEnabledAtom } from '@/jotai/screen';
import { Panel, StatusBadge } from '@/components/ui/settings.tsx';

import { Hdmi } from '../device/hdmi';
import { VideoForm } from './form';
import { useVideoSettings } from './use-video-settings';

export const VideoSettings = ({ setIsLocked }: { setIsLocked: (locked: boolean) => void }) => {
  const { t } = useTranslation();
  const [retryingChroma, setRetryingChroma] = useState(false);
  const enabled = useAtomValue(isHdmiEnabledAtom);
  const { admin, status, caps, browser, saved, failed, refresh } = useVideoSettings();

  const size = (w?: number, h?: number) => (w && h ? `${w} × ${h}` : '—');
  const transport = saved
    ? { direct: 'Direct', webrtc: 'WebRTC', mjpeg: 'MJPEG' }[saved.transport]
    : '';
  const codec =
    saved && saved.transport !== 'mjpeg' ? (saved.codec === 'h265' ? 'H.265' : 'H.264') : '';
  const mjpeg = saved?.transport === 'mjpeg';
  const outputWidth = mjpeg ? status?.mjpegOutputWidth : status?.videoOutputWidth;
  const outputHeight = mjpeg ? status?.mjpegOutputHeight : status?.videoOutputHeight;
  const monitor = saved && caps ? monitorTarget(saved, caps) : undefined;
  const line = (label: string, value: React.ReactNode) => (
    <div className="flex justify-between gap-3 py-1">
      <span className="text-fg-muted">{label}</span>
      <span className="text-right">{value}</span>
    </div>
  );

  return (
    <div className="space-y-6 pb-6">
      <div>
        <p className="text-fg-muted text-sm">{t('videoSettings.description')}</p>
      </div>
      <Hdmi />
      {failed && (
        <Alert
          type="warning"
          showIcon
          message={t('videoSettings.statusFailed')}
          action={<Button onClick={() => void refresh()}>{t('videoSettings.retry')}</Button>}
        />
      )}
      {caps?.monitor.powerCyclePending && (
        <Alert
          type="warning"
          showIcon
          message={t('videoSettings.powerCyclePending')}
          description={t('videoSettings.powerCyclePendingHint')}
          action={
            <Button
              disabled={!admin}
              onClick={() =>
                Modal.confirm({
                  title: t('videoSettings.powerCycleAck'),
                  content: t('videoSettings.powerCycleAckConfirm'),
                  onOk: async () => {
                    const rsp = await updateScreen('monitor_power_cycle_ack', 1, true);
                    if (rsp.code !== 0) {
                      message.error(rsp.msg || t('videoSettings.failed'));
                      throw new Error(rsp.msg);
                    }
                    await refresh();
                  }
                })
              }
            >
              {t('videoSettings.powerCycleAck')}
            </Button>
          }
        />
      )}
      {status?.gopModeRestartRequired && (
        <Alert
          type="warning"
          showIcon
          message={t('videoSettings.gopModePendingReboot')}
          description={t('videoSettings.gopModePendingRebootHint', {
            active: status.gopModeActive === 1 ? 'SmartP' : 'NormalP',
            selected: status.gopMode === 1 ? 'SmartP' : 'NormalP'
          })}
        />
      )}
      {mjpeg &&
        status?.mjpegChroma === 422 &&
        (status.mjpegChromaActive !== 422 || status.mjpegChromaFallback === 'resolution') && (
          <Alert
            type={status.mjpegChromaFallback === 'hardware' ? 'warning' : 'info'}
            showIcon
            message={t(
              `videoSettings.mjpegChromaFallback_${status.mjpegChromaFallback || 'pending'}`
            )}
            action={
              status.mjpegChromaFallback === 'hardware' && admin ? (
                <Button
                  loading={retryingChroma}
                  onClick={async () => {
                    if (retryingChroma) return;
                    setRetryingChroma(true);
                    try {
                      const rsp = await updateScreen('mjpeg_chroma', 422);
                      if (rsp.code !== 0) throw new Error(rsp.msg || t('videoSettings.failed'));
                      await refresh();
                    } catch (error) {
                      message.error(
                        error instanceof Error ? error.message : t('videoSettings.failed')
                      );
                    } finally {
                      setRetryingChroma(false);
                    }
                  }}
                >
                  {t('videoSettings.retry')}
                </Button>
              ) : undefined
            }
          />
        )}
      <div aria-live="polite">
        <Panel className="text-sm">
          <div className="mb-3 flex items-center justify-between">
            <span className="font-medium">{t('videoSettings.current')}</span>
            <StatusBadge tone={enabled ? 'success' : 'warning'}>
              {t(enabled ? 'videoSettings.captureOn' : 'videoSettings.captureOff')}
            </StatusBadge>
          </div>
          {line(
            t('videoSettings.input'),
            enabled && status
              ? `${size(status.inputWidth, status.inputHeight)}${caps?.input.fps ? ` · ${t('videoSettings.hz', { value: nominalRate(caps.input.fps) })}` : ''}`
              : '—'
          )}
          {line(
            t('videoSettings.output'),
            enabled && status
              ? `${size(outputWidth || status.outputWidth, outputHeight || status.outputHeight)} · ${[codec, transport].filter(Boolean).join(' ')} · ${t('videoSettings.fpsValue', { value: status.measuredFps })}`
              : '—'
          )}
          {caps?.monitor.programmable &&
            monitor &&
            line(
              t('videoSettings.monitor'),
              `${saved?.portrait ? t('videoSettings.portrait') : saved?.monitor ? '' : t('videoSettings.automatic')}${
                monitor.width ? ` ${size(monitor.width, monitor.height)}` : ''
              }${caps.monitor.refreshHz || monitor.refresh ? ` · ${t('videoSettings.hz', { value: caps.monitor.refreshHz || monitor.refresh })}` : ''}`.trim()
            )}
          {enabled && status && status.effectiveFps < status.fps && (
            <p className="text-warning mt-3 text-xs">
              {t('videoSettings.fpsLimited', { fps: status.effectiveFps })}
            </p>
          )}
        </Panel>
      </div>
      {caps && browser && saved && (
        <VideoForm
          caps={caps}
          browser={browser}
          saved={saved}
          admin={admin}
          refresh={refresh}
          setIsLocked={setIsLocked}
        />
      )}
    </div>
  );
};
