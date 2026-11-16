import { useCallback, useEffect, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  ColorPicker,
  Input,
  InputNumber,
  message,
  Progress,
  Segmented,
  Slider,
  Switch
} from 'antd';
import clsx from 'clsx';
import { useAtomValue } from 'jotai';
import { PlusIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getScreen, getWatermark, setWatermark, type WatermarkState } from '@/api/vm';
import { screenshotSourceAtom } from '@/jotai/screen.ts';
import { Panel, StatusBadge } from '@/components/ui/settings.tsx';

import {
  canvasPngBase64,
  DEFAULT_DESIGN,
  DESIGN_MAX_BYTES,
  designJson,
  FONT_SIZE,
  limitText,
  loadLogo,
  MARGIN,
  MAX_LINE_LENGTH,
  MAX_LINES,
  OPACITY,
  overlaps,
  parseDesigns,
  permilleAt,
  pictureKey,
  placeBlock,
  pngSize,
  renderWatermark,
  type WatermarkBackground,
  type WatermarkDesign
} from './watermark-render';

// The quick places, row by row: x and y in permille.
const PLACES = [
  'topLeft',
  'top',
  'topRight',
  'left',
  'center',
  'right',
  'bottomLeft',
  'bottom',
  'bottomRight'
] as const;
const placeOf = (index: number) => ({ x: (index % 3) * 500, y: Math.floor(index / 3) * 500 });
// Where new blocks go: the first of these places no block uses yet.
const NEW_BLOCK_PLACES = [8, 0, 2, 6, 7, 1, 5, 3, 4];

// The preview's picture before the device has said what it streams.
const DEFAULT_PICTURE = { width: 1920, height: 1080 };

// A placeholder for a block without a picture, in reference pixels.
const EMPTY_SIZE = { width: 240, height: 60 };

type Block = {
  // React key.
  id: number;
  x: number;
  y: number;
  design: WatermarkDesign;
  logo: HTMLImageElement | null;
  // The saved block this one is (its index then), null for a new block.
  from: number | null;
  // The saved picture's logo, not available here, is to be left out.
  dropSavedLogo: boolean;
};

type Draft = { enabled: boolean; margin: number; blocks: Block[] };

let blockIds = 0;

// The editor for the saved watermark; logos[i] is saved block i's logo when
// this page has it.
function draftOf(saved: WatermarkState, logos: (HTMLImageElement | null)[]): Draft {
  const designs = parseDesigns(saved.design, saved.blocks.length);
  return {
    enabled: saved.enabled,
    margin: saved.margin,
    blocks: saved.blocks.map((b, i) => ({
      id: ++blockIds,
      x: b.x,
      y: b.y,
      design: designs[i],
      logo: logos[i] ?? null,
      from: i,
      dropSavedLogo: false
    }))
  };
}

type Rendered = { url: string; canvas: HTMLCanvasElement; scaled: boolean } | null;

/*
 * The watermark: up to maxBlocks blocks of text with an optional logo, which
 * the device draws into every stream, screenshot and recording. Each block is
 * rendered here at the reference scale and sent as a PNG only when its picture
 * changed or its index did; places and the margin are applied by the device.
 */
export const WatermarkPage = () => {
  const { t } = useTranslation();
  const tr = (key: string, options: Record<string, unknown> = {}) =>
    t(`videoSettings.watermark.${key}`, options);
  const [saved, setSaved] = useState<WatermarkState>();
  const [savedLogos, setSavedLogos] = useState<(HTMLImageElement | null)[]>([]);
  const [loadFailed, setLoadFailed] = useState(false);
  const [draft, setDraft] = useState<Draft>();
  const [selected, setSelected] = useState(0);
  // The size of the current stream (else the input): the preview's shape.
  const [picture, setPicture] = useState(DEFAULT_PICTURE);
  // The stream playing behind the settings, and a still of it (object URL).
  const source = useAtomValue(screenshotSourceAtom);
  const [still, setStill] = useState<string>();
  const [busy, setBusy] = useState(false);
  const file = useRef<HTMLInputElement>(null);
  const frame = useRef<HTMLDivElement>(null);
  const drag = useRef<{ id: number; grabX: number; grabY: number } | null>(null);
  // Rendered pictures by design object: a drag does not render again.
  const renders = useRef(
    new WeakMap<WatermarkDesign, { logo: HTMLImageElement | null; result: Rendered }>()
  );

  const accept = useCallback((data: WatermarkState, logos: (HTMLImageElement | null)[]) => {
    setSaved(data);
    setSavedLogos(logos);
    setDraft(draftOf(data, logos));
    setSelected((index) => Math.max(0, Math.min(index, data.blocks.length - 1)));
  }, []);

  const load = useCallback(
    async (logos: (HTMLImageElement | null)[] = []) => {
      try {
        const rsp = await getWatermark();
        if (rsp.code !== 0) throw new Error(rsp.msg);
        setLoadFailed(false);
        accept(rsp.data, logos);
      } catch {
        setLoadFailed(true);
      }
    },
    [accept]
  );

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    void getScreen()
      .then((rsp) => {
        if (rsp.code !== 0) return;
        const s = rsp.data;
        const size = [
          [s.videoOutputWidth, s.videoOutputHeight],
          [s.outputWidth, s.outputHeight],
          [s.inputWidth, s.inputHeight]
        ].find(([w, h]) => w > 0 && h > 0);
        if (size) setPicture({ width: size[0], height: size[1] });
      })
      .catch(() => undefined);
  }, []);

  // A still of the stream behind the settings, as the preview's background:
  // taken when the page opens with the watermark on, when it is turned on and
  // on request. It shows the watermark the device draws now.
  const grab = useCallback(async () => {
    if (!source) return;
    try {
      const blob = await source.capture();
      setStill((old) => {
        if (old) URL.revokeObjectURL(old);
        return URL.createObjectURL(blob);
      });
    } catch {
      // No video in the page: the plain background stays.
    }
  }, [source]);
  const enabled = draft?.enabled === true;
  useEffect(() => {
    if (enabled) void grab();
  }, [enabled, grab]);
  useEffect(() => () => setStill((old) => (old && URL.revokeObjectURL(old), undefined)), []);

  if (!saved || !draft) {
    return loadFailed ? (
      <Alert
        type="warning"
        showIcon
        title={tr('loadFailed')}
        action={<Button onClick={() => void load()}>{t('videoSettings.retry')}</Button>}
      />
    ) : null;
  }

  const savedDesigns = parseDesigns(saved.design, saved.blocks.length);
  const render = (block: Block): Rendered => {
    const cached = renders.current.get(block.design);
    if (cached && cached.logo === block.logo) return cached.result;
    const made = renderWatermark(block.design, block.logo, saved.maxWidth, saved.maxHeight);
    const result = made && { ...made, url: made.canvas.toDataURL('image/png') };
    renders.current.set(block.design, { logo: block.logo, result });
    return result;
  };

  // Each block's state: whether its picture changed, and the picture shown.
  const blocks = draft.blocks.map((block, index) => {
    const from = block.from;
    const savedImage = from === null ? '' : saved.blocks[from]?.image || '';
    const changed =
      from === null ||
      !savedImage ||
      block.dropSavedLogo ||
      block.logo !== (savedLogos[from] ?? null) ||
      pictureKey(block.design) !== pictureKey(savedDesigns[from] ?? DEFAULT_DESIGN);
    const rendered = changed ? render(block) : null;
    const savedSize = changed ? undefined : pngSize(savedImage);
    const picture = rendered
      ? { url: rendered.url, width: rendered.canvas.width, height: rendered.canvas.height }
      : savedSize && { url: `data:image/png;base64,${savedImage}`, ...savedSize };
    const logoMissing =
      from !== null &&
      !!savedDesigns[from]?.hasLogo &&
      !savedLogos[from] &&
      !block.logo &&
      !block.dropSavedLogo;
    const moved = from !== index;
    const placeChanged =
      from === null || saved.blocks[from]?.x !== block.x || saved.blocks[from]?.y !== block.y;
    return {
      block,
      index,
      changed,
      rendered,
      savedImage,
      picture,
      logoMissing,
      moved,
      placeChanged
    };
  });

  const area = blocks.reduce(
    (sum, b) => sum + (b.picture ? b.picture.width * b.picture.height : 0),
    0
  );
  const designs = blocks.map((b) =>
    b.changed ? { ...b.block.design, hasLogo: !!b.block.logo } : b.block.design
  );
  const designBytes = new TextEncoder().encode(designJson(designs)).length;
  const emptyBlocks = blocks.filter((b) => !b.picture).map((b) => b.index + 1);
  // Off, the editor is hidden: turning it off keeps the saved blocks as they
  // are, and edits made before are not saved.
  const dirty = !draft.enabled
    ? saved.enabled
    : !saved.enabled ||
      draft.margin !== saved.margin ||
      draft.blocks.length !== saved.blocks.length ||
      blocks.some((b) => b.changed || b.moved || b.placeChanged);
  const issue = !draft.enabled
    ? undefined
    : emptyBlocks.length > 0
      ? tr('emptyBlocks', { blocks: emptyBlocks.join(', ') })
      : draft.enabled && draft.blocks.length === 0
        ? tr('needsBlock')
        : area > saved.maxArea
          ? tr('areaOver')
          : designBytes > DESIGN_MAX_BYTES
            ? tr('designTooLong')
            : undefined;

  // The preview frame in reference pixels and each block's place in it.
  const ratio = picture.width / picture.height;
  const frameWidth = saved.refWidth;
  const frameHeight = frameWidth / ratio;
  const rects = blocks.map((b) => {
    const size = b.picture ?? EMPTY_SIZE;
    return {
      x: placeBlock(frameWidth, size.width, b.block.x, draft.margin),
      y: placeBlock(frameHeight, size.height, b.block.y, draft.margin),
      width: size.width,
      height: size.height
    };
  });
  const overlapping = overlaps(rects.map((rect, i) => (blocks[i].picture ? rect : undefined)));

  const current = blocks[selected];
  const block = current?.block;

  function updateBlock(id: number, change: (block: Block) => Block) {
    setDraft((d) => d && { ...d, blocks: d.blocks.map((b) => (b.id === id ? change(b) : b)) });
  }
  function changeDesign<K extends keyof WatermarkDesign>(key: K, value: WatermarkDesign[K]) {
    if (!block) return;
    updateBlock(block.id, (b) => ({ ...b, design: { ...b.design, [key]: value } }));
  }

  function addBlock(at?: { x: number; y: number }) {
    if (!draft || !saved || draft.blocks.length >= saved.maxBlocks) return;
    const used = new Set(draft.blocks.map((b) => `${b.x},${b.y}`));
    const place =
      at ??
      placeOf(NEW_BLOCK_PLACES.find((i) => !used.has(`${placeOf(i).x},${placeOf(i).y}`)) ?? 4);
    const added: Block = {
      id: ++blockIds,
      ...place,
      design: { ...DEFAULT_DESIGN, ...(block && { ...block.design, text: '', hasLogo: false }) },
      logo: null,
      from: null,
      dropSavedLogo: false
    };
    setDraft({ ...draft, blocks: [...draft.blocks, added] });
    setSelected(draft.blocks.length);
  }

  function deleteBlock() {
    if (!draft || !block) return;
    setDraft({ ...draft, blocks: draft.blocks.filter((b) => b.id !== block.id) });
    setSelected((index) => Math.max(0, index - 1));
  }

  async function chooseLogo(f: File) {
    if (!block) return;
    const id = block.id;
    try {
      const logo = await loadLogo(f);
      updateBlock(id, (b) => ({ ...b, logo }));
    } catch {
      message.error(tr('logoError'));
    }
  }

  // Pointer position in reference pixels of the frame.
  function framePoint(e: React.PointerEvent) {
    const box = frame.current?.getBoundingClientRect();
    if (!box || !box.width) return undefined;
    return {
      x: ((e.clientX - box.left) / box.width) * frameWidth,
      y: ((e.clientY - box.top) / box.height) * frameHeight
    };
  }

  function startDrag(e: React.PointerEvent, index: number) {
    if (busy) return;
    const point = framePoint(e);
    if (!point) return;
    e.preventDefault();
    e.currentTarget.setPointerCapture(e.pointerId);
    setSelected(index);
    drag.current = {
      id: blocks[index].block.id,
      grabX: point.x - rects[index].x,
      grabY: point.y - rects[index].y
    };
  }

  function moveDrag(e: React.PointerEvent, index: number) {
    const state = drag.current;
    const point = framePoint(e);
    if (!state || state.id !== blocks[index].block.id || !point || !draft) return;
    const { width, height } = rects[index];
    const x = permilleAt(frameWidth, width, point.x - state.grabX, draft.margin);
    const y = permilleAt(frameHeight, height, point.y - state.grabY, draft.margin);
    if (x !== blocks[index].block.x || y !== blocks[index].block.y)
      updateBlock(state.id, (b) => ({ ...b, x, y }));
  }

  async function apply() {
    if (!saved || !draft || busy || !dirty || issue) return;
    setBusy(true);
    const logos = draft.enabled ? draft.blocks.map((b) => b.logo) : savedLogos;
    try {
      const body = !draft.enabled
        ? {
            enabled: false,
            margin: saved.margin,
            design: saved.design,
            blocks: saved.blocks.map((b) => ({ x: b.x, y: b.y }))
          }
        : {
            enabled: draft.enabled,
            margin: draft.margin,
            design: designJson(designs),
            blocks: await Promise.all(
              blocks.map(async (b) => {
                const place = { x: b.block.x, y: b.block.y };
                // A new picture; or the saved one again, at its new index.
                if (b.rendered)
                  return { ...place, image: await canvasPngBase64(b.rendered.canvas) };
                if (b.moved) return { ...place, image: b.savedImage };
                return place;
              })
            )
          };
      const rsp = await setWatermark(body);
      if (rsp.code === 0) {
        accept(rsp.data, logos);
        message.success(tr('applied'));
      } else {
        // -3: saved, but not applied; show what the device has now.
        if (rsp.code === -3) await load(logos);
        message.error(rsp.msg || tr('failed'));
      }
    } catch {
      message.error(tr('failed'));
    } finally {
      setBusy(false);
    }
  }

  function discard() {
    if (!saved) return;
    setDraft(draftOf(saved, savedLogos));
    setSelected(0);
  }

  const row = (label: string, control: React.ReactNode, note?: React.ReactNode) => (
    <div className="grid items-start gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(200px,1.6fr)]">
      <span className="text-fg pt-1 text-sm">{label}</span>
      <div className="flex flex-col gap-1">
        {control}
        {note && <span className="text-fg-muted text-xs leading-relaxed">{note}</span>}
      </div>
    </div>
  );
  const percent = (value: number, of: number) => `${(value / of) * 100}%`;
  const blockName = (index: number) => tr('blockName', { number: index + 1 });

  return (
    <div className="space-y-6 pb-6">
      <p className="text-fg-muted m-0 text-sm">{tr('description')}</p>
      <Panel className="space-y-4">
        <div className="flex items-center justify-between gap-3">
          <h3 className="m-0 text-sm font-medium">{tr('title')}</h3>
          <StatusBadge tone={saved.enabled ? 'success' : 'neutral'}>
            {tr(saved.enabled ? 'statusOn' : 'statusOff')}
          </StatusBadge>
        </div>
        {saved.enabled && saved.missing > 0 && (
          <Alert type="warning" showIcon title={tr('missing', { count: saved.missing })} />
        )}
        {row(
          tr('enabled'),
          <Switch
            className="self-start"
            aria-label={tr('enabled')}
            checked={draft.enabled}
            disabled={busy}
            onChange={(enabled) => setDraft({ ...draft, enabled })}
          />,
          tr('note')
        )}
        {draft.enabled &&
          row(
            tr('margin'),
            <InputNumber
              aria-label={tr('margin')}
              min={MARGIN.min}
              max={MARGIN.max}
              precision={0}
              suffix="px"
              value={draft.margin}
              disabled={busy}
              onChange={(margin) => margin !== null && setDraft({ ...draft, margin })}
            />,
            tr('marginHint', { width: saved.refWidth })
          )}
      </Panel>

      {draft.enabled && (
        <>
          <section className="space-y-2">
            <div
              ref={frame}
              className="relative mx-auto w-full touch-none overflow-hidden rounded bg-neutral-700 select-none"
              style={{
                aspectRatio: `${ratio}`,
                maxWidth: `calc(70vh * ${ratio})`,
                ...(still && {
                  backgroundImage: `url(${still})`,
                  backgroundSize: '100% 100%'
                })
              }}
              role="group"
              aria-label={tr('preview')}
            >
              {blocks.map((b, index) => {
                const rect = rects[index];
                const style = {
                  left: percent(rect.x, frameWidth),
                  top: percent(rect.y, frameHeight),
                  width: percent(rect.width, frameWidth),
                  height: percent(rect.height, frameHeight)
                };
                const handlers = {
                  onPointerDown: (e: React.PointerEvent) => startDrag(e, index),
                  onPointerMove: (e: React.PointerEvent) => moveDrag(e, index),
                  onPointerUp: () => (drag.current = null),
                  onPointerCancel: () => (drag.current = null)
                };
                const ring =
                  index === selected ? 'outline-2 outline-offset-2 outline-blue-400' : '';
                return b.picture ? (
                  <img
                    key={b.block.id}
                    src={b.picture.url}
                    alt={blockName(index)}
                    draggable={false}
                    className={clsx('absolute max-w-none cursor-move outline', ring || 'outline-0')}
                    style={style}
                    {...handlers}
                  />
                ) : (
                  <div
                    key={b.block.id}
                    className={clsx(
                      'absolute flex cursor-move items-center justify-center border border-dashed border-neutral-400 text-xs text-neutral-300',
                      ring && `outline ${ring}`
                    )}
                    style={style}
                    {...handlers}
                  >
                    {blockName(index)}
                  </div>
                );
              })}
            </div>
            <div className="flex flex-wrap items-start justify-between gap-2">
              <p className="text-fg-muted m-0 text-xs">
                {tr('previewHint', { width: picture.width, height: picture.height })}
              </p>
              {source && (
                <Button size="small" disabled={busy} onClick={() => void grab()}>
                  {tr('refreshStill')}
                </Button>
              )}
            </div>
            {overlapping.length > 0 && (
              <p className="text-warning m-0 text-xs" role="status">
                {tr('overlap', {
                  blocks: overlapping.map(([a, b]) => `${a + 1}–${b + 1}`).join(', ')
                })}
              </p>
            )}
            <div className="flex items-center gap-3">
              <span className="text-fg-muted shrink-0 text-xs">{tr('area')}</span>
              <Progress
                className="m-0 flex-1"
                size="small"
                percent={Math.round((area / saved.maxArea) * 100)}
                status={area > saved.maxArea ? 'exception' : 'normal'}
                format={() => `${area.toLocaleString()} / ${saved.maxArea.toLocaleString()}`}
              />
            </div>
          </section>

          <div className="flex flex-wrap items-start gap-4">
            <div className="flex flex-col gap-1">
              <div className="grid w-fit grid-cols-3 gap-1" role="group" aria-label={tr('places')}>
                {PLACES.map((name, index) => {
                  const place = placeOf(index);
                  // The blocks at this place, by index.
                  const here = blocks
                    .filter((b) => b.block.x === place.x && b.block.y === place.y)
                    .map((b) => b.index);
                  const pressed = here.includes(selected);
                  const full = draft.blocks.length >= saved.maxBlocks;
                  return (
                    <Button
                      key={name}
                      size="small"
                      className="min-w-9 tabular-nums"
                      type={pressed ? 'primary' : 'default'}
                      aria-label={tr(`anchor.${name}`)}
                      aria-pressed={pressed}
                      title={tr(`anchor.${name}`)}
                      disabled={busy || (!here.length && full)}
                      onClick={() =>
                        here.length
                          ? // The next block here, when one of them is selected.
                            setSelected(here[(here.indexOf(selected) + 1) % here.length])
                          : addBlock(place)
                      }
                    >
                      {here.length ? (
                        here.map((i) => i + 1).join(',')
                      ) : (
                        <PlusIcon size={12} className="opacity-40" aria-hidden />
                      )}
                    </Button>
                  );
                })}
              </div>
              <span className="text-fg-muted max-w-48 text-xs">{tr('placesHint')}</span>
            </div>
            <div
              className="flex flex-wrap items-center gap-2"
              role="group"
              aria-label={tr('blocks')}
            >
              {blocks.map((b, index) => (
                <Button
                  key={b.block.id}
                  type={index === selected ? 'primary' : 'default'}
                  aria-pressed={index === selected}
                  disabled={busy}
                  onClick={() => setSelected(index)}
                >
                  {blockName(index)}
                </Button>
              ))}
              <Button
                icon={<PlusIcon size={14} />}
                disabled={busy || draft.blocks.length >= saved.maxBlocks}
                onClick={() => addBlock()}
              >
                {tr('addBlock')}
              </Button>
            </div>
          </div>
          {!block ? (
            <p className="text-fg-muted m-0 text-xs">{tr('noBlocks')}</p>
          ) : (
            <Panel className="space-y-4">
              {row(
                tr('text'),
                <Input.TextArea
                  aria-label={tr('text')}
                  value={block.design.text}
                  placeholder={tr('textPlaceholder')}
                  autoSize={{ minRows: 1, maxRows: MAX_LINES }}
                  disabled={busy}
                  onChange={(e) => changeDesign('text', limitText(e.target.value))}
                />,
                tr('textHint', { lines: MAX_LINES, length: MAX_LINE_LENGTH })
              )}
              {row(
                tr('fontSize'),
                <div className="flex flex-wrap items-center gap-3">
                  <InputNumber
                    aria-label={tr('fontSize')}
                    min={FONT_SIZE.min}
                    max={FONT_SIZE.max}
                    precision={0}
                    suffix="px"
                    value={block.design.fontSize}
                    disabled={busy}
                    onChange={(value) => value !== null && changeDesign('fontSize', value)}
                  />
                  <Checkbox
                    checked={block.design.bold}
                    disabled={busy}
                    onChange={(e) => changeDesign('bold', e.target.checked)}
                  >
                    {tr('bold')}
                  </Checkbox>
                </div>,
                tr('fontSizeHint')
              )}
              {row(
                tr('color'),
                <ColorPicker
                  className="self-start"
                  aria-label={tr('color')}
                  value={block.design.color}
                  disabledAlpha
                  showText
                  disabled={busy}
                  onChange={(color) => changeDesign('color', color.toHexString().toUpperCase())}
                />
              )}
              {row(
                tr('background'),
                <Segmented<WatermarkBackground>
                  className="self-start"
                  aria-label={tr('background')}
                  value={block.design.background}
                  disabled={busy}
                  onChange={(value) => changeDesign('background', value)}
                  options={(['none', 'dark', 'light'] as const).map((value) => ({
                    value,
                    label: tr(`background_${value}`)
                  }))}
                />
              )}
              {row(
                tr('opacity'),
                <div className="flex items-center gap-3">
                  <Slider
                    className="flex-1"
                    aria-label={tr('opacity')}
                    min={OPACITY.min}
                    max={OPACITY.max}
                    value={block.design.opacity}
                    disabled={busy}
                    onChange={(value) => changeDesign('opacity', value)}
                  />
                  <span className="w-10 text-right text-sm tabular-nums">
                    {block.design.opacity}%
                  </span>
                </div>
              )}
              {row(
                tr('logo'),
                <>
                  <div className="flex flex-wrap gap-2">
                    <Button disabled={busy} onClick={() => file.current?.click()}>
                      {tr(block.logo ? 'logoReplace' : 'logoChoose')}
                    </Button>
                    {(block.logo || current.logoMissing) && (
                      <Button
                        disabled={busy}
                        onClick={() =>
                          updateBlock(block.id, (b) => ({
                            ...b,
                            logo: null,
                            dropSavedLogo: current.logoMissing || b.dropSavedLogo
                          }))
                        }
                      >
                        {tr('logoRemove')}
                      </Button>
                    )}
                  </div>
                  <input
                    ref={file}
                    type="file"
                    className="hidden"
                    accept="image/png,image/jpeg,image/svg+xml,image/webp"
                    onChange={(e) => {
                      const f = e.target.files?.[0];
                      e.target.value = '';
                      if (f) void chooseLogo(f);
                    }}
                  />
                </>,
                current.logoMissing ? (
                  <span className={current.changed ? 'text-warning' : undefined}>
                    {tr(current.changed ? 'logoDropped' : 'logoSaved')}
                  </span>
                ) : (
                  tr('logoHint')
                )
              )}
              {current.rendered?.scaled && (
                <p className="text-warning m-0 text-xs" role="status">
                  {tr('scaled', { width: saved.maxWidth, height: saved.maxHeight })}
                </p>
              )}
              <Button
                danger
                className="self-start"
                icon={<Trash2Icon size={14} />}
                disabled={busy}
                onClick={deleteBlock}
              >
                {tr('deleteBlock')}
              </Button>
            </Panel>
          )}
        </>
      )}

      <div className="border-line sticky bottom-0 z-10 flex flex-wrap items-center gap-3 border-t bg-neutral-900 py-3">
        <Button
          type="primary"
          loading={busy}
          disabled={!dirty || !!issue}
          onClick={() => void apply()}
        >
          {t('videoSettings.apply')}
        </Button>
        <Button disabled={!dirty || busy} onClick={discard}>
          {t('videoSettings.discard')}
        </Button>
        <span className={clsx('text-xs', issue ? 'text-warning' : 'text-fg-muted')} role="status">
          {dirty ? (issue ?? t('videoSettings.pending')) : ''}
        </span>
      </div>
    </div>
  );
};
