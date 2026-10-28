import { useCallback, useEffect, useRef, useState, type PointerEvent } from 'react';
import { Alert, Button, Card, Input, message, Modal, Select, Space, Spin, theme } from 'antd';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { getScreenText } from '@/api/vm.ts';
import { writeClipboardText } from '@/lib/clipboard.ts';
import { getScreenTextLanguage, setScreenTextLanguage } from '@/lib/localstorage.ts';
import {
  boxesInRegion,
  boxesToText,
  screenTextLanguages,
  type ScreenTextLanguage,
  type ScreenTextRegion,
  type ScreenTextResult
} from '@/lib/screen-text.ts';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { resolutionAtom, screenTextOpenAtom } from '@/jotai/screen.ts';

import {
  getMediaSize,
  getRenderedMediaRect,
  isSameGeometry,
  type RenderedMediaRect
} from './geometry.ts';

type Phase = 'select' | 'reading' | 'result';

const wholeScreen: ScreenTextRegion = { left: 0, top: 0, width: 1, height: 1 };
const minimumSelectionSize = 8;

// Server answers that have their own message (server/service/vm/screen_text.go).
const errorKeys: Record<number, string> = {
  [-2]: 'screenText.busy',
  [-3]: 'screenText.noSignal',
  [-5]: 'screenText.tooLarge'
};

// Reads the text on the screen with the TPU add-on: the user drags a
// rectangle over the video (or takes the whole screen); the server reads that
// area with a margin, and the words whose centre is in the rectangle are kept.
export const ScreenTextOverlay = () => {
  const { t } = useTranslation();
  const { token } = theme.useToken();
  const [open, setOpen] = useAtom(screenTextOpenAtom);
  const resolution = useAtomValue(resolutionAtom);
  const setKeyboardLock = useSetAtom(keyboardLockAtom);
  const [phase, setPhase] = useState<Phase>('select');
  const [language, setLanguage] = useState<ScreenTextLanguage>(getScreenTextLanguage);
  const [frame, setFrame] = useState<RenderedMediaRect | null>(null);
  const [selection, setSelection] = useState<RenderedMediaRect | null>(null);
  const [region, setRegion] = useState<ScreenTextRegion>(wholeScreen);
  const [text, setText] = useState('');
  const [seconds, setSeconds] = useState(0);
  const [error, setError] = useState('');
  const dragStart = useRef<{ x: number; y: number } | null>(null);
  const frameRef = useRef<RenderedMediaRect | null>(null);
  const request = useRef<AbortController | null>(null);

  const close = useCallback(() => {
    request.current?.abort();
    request.current = null;
    dragStart.current = null;
    setSelection(null);
    setError('');
    setText('');
    setPhase('select');
    setOpen(false);
  }, [setOpen]);

  // Keys go to the dialog, not to the remote computer, while it is open.
  useEffect(() => {
    if (!open) return;
    setKeyboardLock({ source: 'screen-text', locked: true });
    return () => setKeyboardLock({ source: 'screen-text', locked: false });
  }, [open, setKeyboardLock]);

  // Where the picture is drawn on the page (the video keeps its aspect ratio).
  useEffect(() => {
    if (!open || phase !== 'select') return;
    function update() {
      const screen = document.getElementById('screen');
      const size = screen && getMediaSize(screen, resolution);
      const bounds = screen?.getBoundingClientRect();
      const next =
        size && bounds && bounds.width > 0 && bounds.height > 0
          ? getRenderedMediaRect(bounds, size)
          : null;
      if (isSameGeometry(frameRef.current, next)) return;
      // The selection is in page pixels: it no longer covers the same text.
      if (frameRef.current) {
        dragStart.current = null;
        setSelection(null);
      }
      frameRef.current = next;
      setFrame(next);
    }
    update();
    window.addEventListener('resize', update);
    const timer = window.setInterval(update, 250);
    return () => {
      window.removeEventListener('resize', update);
      window.clearInterval(timer);
    };
  }, [open, phase, resolution]);

  useEffect(() => {
    if (!open || phase !== 'select') return;
    function onKeyDown(event: KeyboardEvent) {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      event.stopImmediatePropagation();
      close();
    }
    document.addEventListener('keydown', onKeyDown);
    return () => document.removeEventListener('keydown', onKeyDown);
  }, [open, phase, close]);

  // Unmounted (capture switched off): stop the read and do not come back open.
  useEffect(
    () => () => {
      request.current?.abort();
      setOpen(false);
    },
    [setOpen]
  );

  const read = useCallback(
    async (target: ScreenTextRegion, lang: ScreenTextLanguage) => {
      request.current?.abort();
      const controller = new AbortController();
      request.current = controller;
      setRegion(target);
      setError('');
      setPhase('reading');
      try {
        // A selection is read on its own (the server crops it, with a margin);
        // the whole screen without a crop.
        const rsp = await getScreenText(
          lang,
          controller.signal,
          target === wholeScreen ? undefined : target
        );
        if (controller.signal.aborted) return;
        if (rsp.code !== 0) {
          setError(
            errorKeys[rsp.code] ? t(errorKeys[rsp.code]) : rsp.msg || t('screenText.failed')
          );
          setText('');
        } else {
          const result = rsp.data as ScreenTextResult;
          setText(boxesToText(boxesInRegion(result, target)));
          setSeconds(Math.round(result.total_ms / 100) / 10);
        }
        setPhase('result');
      } catch {
        if (controller.signal.aborted) return;
        setError(t('screenText.failed'));
        setText('');
        setPhase('result');
      } finally {
        if (request.current === controller) request.current = null;
      }
    },
    [t]
  );

  function chooseLanguage(lang: ScreenTextLanguage) {
    setLanguage(lang);
    setScreenTextLanguage(lang);
  }

  function clamp(x: number, y: number) {
    if (!frame) return null;
    return {
      x: Math.max(frame.left, Math.min(frame.left + frame.width, x)),
      y: Math.max(frame.top, Math.min(frame.top + frame.height, y))
    };
  }

  function onPointerDown(event: PointerEvent<HTMLDivElement>) {
    const point = clamp(event.clientX, event.clientY);
    if (!point) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragStart.current = point;
    setSelection({ left: point.x, top: point.y, width: 0, height: 0 });
  }

  function onPointerMove(event: PointerEvent<HTMLDivElement>) {
    const start = dragStart.current;
    const point = clamp(event.clientX, event.clientY);
    if (!start || !point) return;
    setSelection({
      left: Math.min(start.x, point.x),
      top: Math.min(start.y, point.y),
      width: Math.abs(point.x - start.x),
      height: Math.abs(point.y - start.y)
    });
  }

  function onPointerUp(event: PointerEvent<HTMLDivElement>) {
    dragStart.current = null;
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }

  const selected =
    !!selection &&
    selection.width >= minimumSelectionSize &&
    selection.height >= minimumSelectionSize;

  function readSelection() {
    if (!frame || !selection || !selected) return;
    void read(
      {
        left: (selection.left - frame.left) / frame.width,
        top: (selection.top - frame.top) / frame.height,
        width: selection.width / frame.width,
        height: selection.height / frame.height
      },
      language
    );
  }

  async function copy() {
    try {
      await writeClipboardText(text);
      message.success(t('screenText.copied'));
    } catch {
      message.error(t('screenText.copyFailed'));
    }
  }

  if (!open) return null;

  const languageSelect = (
    <Select
      size="small"
      value={language}
      onChange={chooseLanguage}
      popupMatchSelectWidth={false}
      options={screenTextLanguages.map((lang) => ({
        value: lang,
        label: t(`screenText.languages.${lang}`)
      }))}
    />
  );

  if (phase === 'result') {
    return (
      <Modal
        open
        title={t('screenText.title')}
        width={720}
        onCancel={close}
        footer={
          <Space wrap>
            {languageSelect}
            <Button onClick={() => void read(region, language)}>{t('screenText.again')}</Button>
            <Button onClick={() => setPhase('select')}>{t('screenText.newArea')}</Button>
            <Button type="primary" disabled={!text} onClick={() => void copy()}>
              {t('screenText.copy')}
            </Button>
          </Space>
        }
      >
        {error ? (
          <Alert type="error" showIcon message={error} />
        ) : text ? (
          <Space direction="vertical" className="w-full">
            <Input.TextArea
              value={text}
              readOnly
              autoSize={{ minRows: 4, maxRows: 18 }}
              className="font-mono"
              onFocus={(event) => event.currentTarget.select()}
            />
            <span style={{ color: token.colorTextSecondary }}>
              {t('screenText.took', { seconds })}
            </span>
          </Space>
        ) : (
          <Alert type="info" showIcon message={t('screenText.empty')} />
        )}
      </Modal>
    );
  }

  return (
    <div
      className="fixed inset-0 z-1100 touch-none select-none"
      style={{ background: token.colorBgMask, cursor: phase === 'select' ? 'crosshair' : 'wait' }}
      onContextMenu={(event) => event.preventDefault()}
      onPointerDown={phase === 'select' ? onPointerDown : undefined}
      onPointerMove={phase === 'select' ? onPointerMove : undefined}
      onPointerUp={phase === 'select' ? onPointerUp : undefined}
      onPointerCancel={phase === 'select' ? onPointerUp : undefined}
    >
      {frame && (
        <div
          className="pointer-events-none fixed border border-dashed"
          style={{ ...frame, borderColor: token.colorTextSecondary }}
        />
      )}
      {selection && phase === 'select' && (
        <div
          className="pointer-events-none fixed border"
          style={{
            ...selection,
            borderColor: token.colorPrimary,
            background: `color-mix(in srgb, ${token.colorPrimary} 18%, transparent)`
          }}
        />
      )}
      <div
        className="fixed top-5 left-1/2 z-1120 max-w-[calc(100%-1rem)] -translate-x-1/2"
        onPointerDown={(event) => event.stopPropagation()}
      >
        <Card
          size="small"
          title={phase === 'select' ? t('screenText.dragHint') : t('screenText.reading')}
          style={{ boxShadow: token.boxShadowSecondary }}
          styles={{ header: { minHeight: 36 }, body: { padding: 8 } }}
        >
          {phase === 'select' ? (
            <Space wrap size="small">
              {languageSelect}
              <Button size="small" type="primary" disabled={!selected} onClick={readSelection}>
                {t('screenText.readSelection')}
              </Button>
              <Button size="small" onClick={() => void read(wholeScreen, language)}>
                {t('screenText.wholeScreen')}
              </Button>
              <Button size="small" onClick={close}>
                {t('screenText.cancel')}
              </Button>
            </Space>
          ) : (
            <Space size="small">
              <Spin size="small" />
              <span>{t('screenText.readingHint')}</span>
              <Button size="small" onClick={close}>
                {t('screenText.cancel')}
              </Button>
            </Space>
          )}
        </Card>
      </div>
    </div>
  );
};
