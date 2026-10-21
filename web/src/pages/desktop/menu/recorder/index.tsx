import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, message, Tooltip } from 'antd';
import { useAtomValue } from 'jotai';
import { useTranslation } from 'react-i18next';

import {
  captureRecordingSource,
  recordingMimeType,
  recordStream,
  withRecordingAudio,
  type RecordingWriter
} from '@/lib/recording';
import { getRecordingAudio } from '@/lib/recording-audio.ts';
import type { RecordingResult } from '@/lib/recording-result.ts';
import { getRecordingSource, watchRecordingSource } from '@/lib/recording-source.ts';
import { getUsbAudioStream, watchUsbAudioStream } from '@/lib/usb-audio-stream.ts';
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
  const [state, setState] = useState<'idle' | 'choosing' | 'waiting' | 'recording' | 'saving'>(
    'idle'
  );
  const [elapsed, setElapsed] = useState(0);
  const recording = useRef<{ stop: () => void } | null>(null);
  const active = useRef(true);
  const generation = useRef(0);
  const busy = useRef(false);
  // The recording includes the USB audio this page plays, if any.
  const [audio, setAudio] = useState(() => getUsbAudioStream());
  useEffect(() => {
    // A stream published before this subscription counts too.
    setAudio(getUsbAudioStream());
    return watchUsbAudioStream(() => setAudio(getUsbAudioStream()));
  }, []);
  // Direct and WebRTC record the stream as received, without re-encoding.
  const [received, setReceived] = useState(() => getRecordingSource());
  useEffect(() => {
    setReceived(getRecordingSource());
    return watchRecordingSource(setReceived);
  }, []);
  const passthroughMode = mode === 'direct' || mode === 'h264';
  const passthrough = passthroughMode && !!received;
  const mime = recordingMimeType();
  const picker = (window as Window & { showSaveFilePicker?: SavePicker }).showSaveFilePicker;
  const supported = window.isSecureContext && !!picker && (passthrough || !!mime);

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
    if (state !== 'recording' && state !== 'waiting' && state !== 'saving') return;
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
    let unwatchAudio = () => {};
    let session: { stop: () => void; done: Promise<RecordingResult> } | undefined;
    try {
      const receivedSource = passthroughMode ? getRecordingSource() : null;
      if (receivedSource) {
        const handle = await picker!.call(window, {
          suggestedName: `NanoKVM-${new Date().toISOString().replace(/[:.]/g, '-')}.mp4`,
          types: [{ description: t('recorder.title'), accept: { 'video/mp4': ['.mp4'] } }]
        });
        if (!active.current || attempt !== generation.current) return;
        writer = await handle.createWritable();
        if (!active.current || attempt !== generation.current) {
          await writer.abort();
          writer = undefined;
          return;
        }
        const { recordPassthrough } = await import('@/lib/passthrough-recording.ts');
        const currentAttempt = () =>
          active.current &&
          attempt === generation.current &&
          getRecordingSource() === receivedSource;
        if (!currentAttempt()) {
          await writer.abort();
          writer = undefined;
          return;
        }
        const audioStream = getUsbAudioStream();
        const sound =
          audioStream
            ?.getAudioTracks()
            .find((track) => track.readyState === 'live')
            ?.clone() ?? null;
        const passthroughSession = await recordPassthrough(
          receivedSource,
          writer as unknown as FileSystemWritableFileStream,
          sound,
          undefined,
          getRecordingAudio(audioStream)
        );
        session = passthroughSession;
        if (!currentAttempt()) {
          passthroughSession.stop();
          await passthroughSession.done.catch(() => {});
          return;
        }
        unwatchAudio = watchUsbAudioStream(() => {
          if (getUsbAudioStream() !== audioStream) passthroughSession.stopAudio();
        });
        if (getUsbAudioStream() !== audioStream) passthroughSession.stopAudio();
        recording.current = passthroughSession;
        setElapsed(0);
        setState('waiting');
        void passthroughSession.started.then(() => {
          if (currentAttempt() && recording.current === passthroughSession) {
            setElapsed(0);
            setState('recording');
          }
        });
        const result = await passthroughSession.done;
        if (active.current) {
          if (result.reason) message.warning(t('recorder.interrupted'));
          else message.success(t('recorder.saved'));
        }
        return;
      }
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
      const sound = getUsbAudioStream();
      withRecordingAudio(source.stream, sound);
      session = recordStream(source.stream, writer, recordingMimeType(!!sound) ?? mime!);
      recording.current = session;
      setElapsed(0);
      setState('recording');
      const result = await session.done;
      if (active.current) {
        if (result.reason) message.warning(t('recorder.interrupted'));
        else message.success(t('recorder.saved'));
      }
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
      unwatchAudio();
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
  const startTitle = passthrough
    ? audio
      ? t('recorder.startDirectWithAudio')
      : t('recorder.startDirect')
    : audio
      ? t('recorder.startWithAudio')
      : t('recorder.start');
  const title = !supported
    ? t('recorder.unsupported')
    : !captureEnabled
      ? t('recorder.captureDisabled')
      : state === 'recording'
        ? t('recorder.stop')
        : state === 'waiting'
          ? t('recorder.waitingKeyframe')
          : state === 'saving'
            ? t('recorder.saving')
            : startTitle;
  return (
    <Tooltip title={title} mouseEnterDelay={0.6}>
      <span className="flex shrink-0">
        <Button
          type="text"
          aria-label={title}
          aria-pressed={state === 'recording' || state === 'waiting'}
          disabled={!supported || !captureEnabled || state === 'choosing' || state === 'saving'}
          className={`flex! h-[30px]! min-w-[30px]! items-center! justify-center! !px-1 [&_.ant-btn-icon]:flex! [&_.ant-btn-icon]:items-center! [&_svg]:block ${!supported || !captureEnabled ? '!text-neutral-500' : state === 'recording' || state === 'waiting' || state === 'saving' ? '!text-red-400 hover:!text-red-300' : '!text-neutral-300 hover:!text-white'}`}
          onClick={state === 'recording' || state === 'waiting' ? stop : () => void start()}
          icon={
            state === 'recording' || state === 'waiting' ? (
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
