import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, message, Tooltip } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import {
  captureRecordingSource,
  recordingMimeType,
  recordStream,
  type RecordingWriter
} from '@/lib/recording';
import { isHdmiEnabledAtom, streamFpsAtom, videoModeAtom } from '@/jotai/screen';

type SavePicker = (options: {
  suggestedName: string;
  types: { description: string; accept: Record<string, string[]> }[];
}) => Promise<{ createWritable(): Promise<RecordingWriter> }>;

export const Recorder = () => {
  const { t } = useTranslation();
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  const fps = useAtomValue(streamFpsAtom);
  const mode = useAtomValue(videoModeAtom);
  const [state, setState] = useState<'idle' | 'choosing' | 'recording' | 'saving'>('idle');
  const [elapsed, setElapsed] = useState(0);
  const recording = useRef<ReturnType<typeof recordStream> | null>(null);
  const active = useRef(true);
  const generation = useRef(0);
  const busy = useRef(false);
  const mime = recordingMimeType();
  const picker = (window as Window & { showSaveFilePicker?: SavePicker }).showSaveFilePicker;
  const supported = window.isSecureContext && !!picker && !!mime;

  const invalidatePending = useCallback(() => {
    generation.current++;
  }, []);

  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
      invalidatePending();
      recording.current?.stop();
    };
  }, [invalidatePending]);
  useEffect(() => {
    generation.current++;
    recording.current?.stop();
  }, [captureEnabled, mode]);
  useEffect(() => {
    if (state !== 'recording' && state !== 'saving') return;
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  }, [state]);
  useEffect(() => {
    if (state !== 'recording') return;
    const start = Date.now();
    const timer = window.setInterval(
      () => setElapsed(Math.floor((Date.now() - start) / 1000)),
      1000
    );
    return () => window.clearInterval(timer);
  }, [state]);

  async function start() {
    if (!supported || !captureEnabled || busy.current) return;
    busy.current = true;
    const attempt = generation.current;
    setState('choosing');
    let writer: RecordingWriter | undefined;
    let source: ReturnType<typeof captureRecordingSource> | undefined;
    let session: ReturnType<typeof recordStream> | undefined;
    try {
      const extension = mime!.includes('mp4') ? '.mp4' : '.webm';
      const handle = await picker!.call(window, {
        suggestedName: `NanoKVM-${new Date().toISOString().replace(/[:.]/g, '-')}${extension}`,
        types: [
          { description: t('recorder.title'), accept: { [mime!.split(';')[0]]: [extension] } }
        ]
      });
      if (!active.current || attempt !== generation.current) return;
      const element = document.getElementById('screen');
      if (!element) throw new Error('recording-no-video');
      writer = await handle.createWritable();
      if (!active.current || attempt !== generation.current) {
        await writer.abort();
        writer = undefined;
        return;
      }
      source = captureRecordingSource(element, Math.min(60, Math.max(1, fps)));
      session = recordStream(source.stream, writer, mime!);
      recording.current = session;
      setElapsed(0);
      setState('recording');
      await session.done;
      if (active.current) message.success(t('recorder.saved'));
    } catch (error) {
      if (!session) await writer?.abort().catch(() => {});
      if (active.current && !(error instanceof DOMException && error.name === 'AbortError')) {
        message.error(
          t(
            error instanceof Error && error.message === 'recording-no-video'
              ? 'recorder.noVideo'
              : 'recorder.failed'
          )
        );
      }
    } finally {
      source?.dispose();
      recording.current = null;
      busy.current = false;
      if (active.current) setState('idle');
    }
  }

  function stop() {
    setState('saving');
    recording.current?.stop();
  }
  const title = !supported
    ? t('recorder.unsupported')
    : !captureEnabled
      ? t('recorder.captureDisabled')
      : state === 'recording'
        ? t('recorder.stop')
        : state === 'saving'
          ? t('recorder.saving')
          : t('recorder.start');
  return (
    <Tooltip title={title} mouseEnterDelay={0.6}>
      <span className="flex shrink-0">
        <Button
          type="text"
          aria-label={title}
          aria-pressed={state === 'recording'}
          disabled={!supported || !captureEnabled || state === 'choosing' || state === 'saving'}
          className={`!flex !h-[30px] !min-w-[30px] !items-center !justify-center !px-1 [&_.ant-btn-icon]:!flex [&_.ant-btn-icon]:!items-center [&_svg]:block ${!supported || !captureEnabled ? '!text-neutral-500' : '!text-red-400 hover:!text-red-300'}`}
          onClick={state === 'recording' ? stop : () => void start()}
          icon={
            state === 'recording' ? (
              <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
                <rect x="4" y="4" width="16" height="16" rx="2" fill="currentColor" />
              </svg>
            ) : (
              <svg width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
                <circle
                  cx="12"
                  cy="12"
                  r="9.5"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                />
                <circle cx="12" cy="12" r="6.5" fill="currentColor" />
              </svg>
            )
          }
        >
          {state === 'recording' && (
            <span className="text-xs tabular-nums">
              {Math.floor(elapsed / 60)}:{String(elapsed % 60).padStart(2, '0')}
            </span>
          )}
        </Button>
      </span>
    </Tooltip>
  );
};
