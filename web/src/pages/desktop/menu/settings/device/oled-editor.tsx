import { KeyboardEvent, PointerEvent, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Dropdown,
  Input,
  InputNumber,
  message,
  Modal,
  Popconfirm,
  Segmented,
  Select,
  Spin,
  Switch
} from 'antd';
import clsx from 'clsx';
import {
  ArrowDownIcon,
  ArrowUpIcon,
  CopyIcon,
  ImageIcon,
  MinusIcon,
  PlusIcon,
  SquareIcon,
  Trash2Icon,
  TypeIcon
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { icons } from '@/lib/oled/font.ts';
import {
  conditions,
  holds,
  imageFromPixels,
  itemBox,
  joinWhen,
  logicalSize,
  maxConditions,
  maxItems,
  maxPages,
  maxText,
  OledItem,
  OledLayout,
  OledValues,
  render,
  sampleValues,
  splitWhen,
  variables
} from '@/lib/oled/layout.ts';
import { showRequestError } from '@/lib/show-request-error.ts';

const KEY = 'settings.device.oled.editor';
const PREVIEW_REFRESH_MS = 5000;

type Props = {
  open: boolean;
  onClose: () => void;
  // The display is off (oled_sleep -1): a preview would not show.
  displayOff: boolean;
};

const copy = (layout: OledLayout): OledLayout => JSON.parse(JSON.stringify(layout)) as OledLayout;

function moved(item: OledItem, dx: number, dy: number): OledItem {
  const next = { ...item, x: item.x + dx, y: item.y + dy };
  if (item.type === 'line') {
    next.x2 = (item.x2 ?? item.x) + dx;
    next.y2 = (item.y2 ?? item.y) + dy;
  }
  return next;
}

// Image rows with every pixel inside the width flipped.
function inverted(item: OledItem): string {
  const width = item.width ?? 1;
  const stride = Math.ceil(width / 8);
  const bytes = (item.data ?? '').match(/../g)?.map((pair) => parseInt(pair, 16)) ?? [];
  return bytes
    .map((byte, index) => {
      const column = (index % stride) * 8;
      const used = Math.min(8, width - column);
      const mask = (0xff << (8 - used)) & 0xff;
      return (byte ^ mask).toString(16).padStart(2, '0');
    })
    .join('');
}

// A picture file scaled to fit the panel, as an image item.
async function pictureItem(file: File, maxWidth: number, maxHeight: number): Promise<OledItem> {
  const bitmap = await createImageBitmap(file);
  const scale = Math.min(1, maxWidth / bitmap.width, maxHeight / bitmap.height);
  const width = Math.max(1, Math.round(bitmap.width * scale));
  const height = Math.max(1, Math.round(bitmap.height * scale));
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext('2d')!;
  context.drawImage(bitmap, 0, 0, width, height);
  bitmap.close();
  const rgba = context.getImageData(0, 0, width, height).data;
  return {
    type: 'image',
    x: 0,
    y: 0,
    width,
    height,
    data: imageFromPixels(rgba, width, height, false)
  };
}

const Preview = ({
  layout,
  values,
  selected,
  onSelect,
  onMove
}: {
  layout: OledLayout;
  values: OledValues;
  selected: number;
  onSelect: (index: number) => void;
  onMove: (dx: number, dy: number) => void;
}) => {
  const canvas = useRef<HTMLCanvasElement>(null);
  const area = useRef<HTMLDivElement>(null);
  const drag = useRef<{ x: number; y: number } | null>(null);
  const frame = useMemo(() => render(layout, values), [layout, values]);
  // With burn-in "roam" only the block is shown and edited: the device
  // moves it around the panel.
  const roam = layout.burnin === 'roam';
  const width = roam ? Math.min(layout.roamWidth ?? frame.width, frame.width) : frame.width;
  const height = roam ? Math.min(layout.roamHeight ?? frame.height, frame.height) : frame.height;
  // The panel at a whole multiple of its pixels, as large as fits.
  const [scale, setScale] = useState(4);

  useEffect(() => {
    const element = area.current;
    if (!element) return;
    const fit = () => setScale(Math.max(1, Math.min(10, Math.floor(element.clientWidth / width))));
    fit();
    const observer = new ResizeObserver(fit);
    observer.observe(element);
    return () => observer.disconnect();
  }, [width]);

  useEffect(() => {
    const context = canvas.current?.getContext('2d');
    if (!context) return;
    const image = context.createImageData(width, height);
    for (let y = 0; y < height; y++)
      for (let x = 0; x < width; x++) {
        const [r, g, b] = frame.pixels[y * frame.width + x] ? [150, 205, 255] : [8, 10, 14];
        image.data.set([r, g, b, 255], (y * width + x) * 4);
      }
    context.putImageData(image, 0, 0);
  }, [frame, width, height]);

  const point = (event: PointerEvent) => {
    const rect = canvas.current!.getBoundingClientRect();
    return {
      x: Math.floor(((event.clientX - rect.left) / rect.width) * width),
      y: Math.floor(((event.clientY - rect.top) / rect.height) * height)
    };
  };

  const down = (event: PointerEvent<HTMLCanvasElement>) => {
    const p = point(event);
    let hit = -1;
    for (let i = layout.items.length - 1; i >= 0; i--) {
      if (!holds(layout.items[i], values)) continue;
      const box = itemBox(layout.items[i], values);
      if (p.x >= box.x && p.x < box.x + box.width && p.y >= box.y && p.y < box.y + box.height) {
        hit = i;
        break;
      }
    }
    onSelect(hit);
    if (hit < 0) return;
    drag.current = p;
    event.currentTarget.setPointerCapture(event.pointerId);
  };

  const move = (event: PointerEvent) => {
    if (!drag.current) return;
    const p = point(event);
    const dx = p.x - drag.current.x;
    const dy = p.y - drag.current.y;
    if (!dx && !dy) return;
    drag.current = p;
    onMove(dx, dy);
  };

  const key = (event: KeyboardEvent) => {
    const step = event.shiftKey ? 8 : 1;
    const moves: Record<string, [number, number]> = {
      ArrowLeft: [-step, 0],
      ArrowRight: [step, 0],
      ArrowUp: [0, -step],
      ArrowDown: [0, step]
    };
    if (selected < 0 || !moves[event.key]) return;
    event.preventDefault();
    onMove(...moves[event.key]);
  };

  const percent = (value: number, total: number) => `${(value / total) * 100}%`;
  // No frame around an item the state previewed does not draw.
  const shown = selected >= 0 && layout.items[selected] && holds(layout.items[selected], values);
  const box = shown ? itemBox(layout.items[selected], values) : null;

  return (
    <div ref={area} className="flex w-full justify-center">
      <div
        className="relative shrink-0 select-none"
        style={{ width: width * scale, height: height * scale }}
      >
        <canvas
          ref={canvas}
          width={width}
          height={height}
          tabIndex={0}
          className="block h-full w-full cursor-crosshair outline-none focus-visible:ring-2"
          style={{ imageRendering: 'pixelated', touchAction: 'none' }}
          onPointerDown={down}
          onPointerMove={move}
          onPointerUp={() => (drag.current = null)}
          onPointerCancel={() => (drag.current = null)}
          onKeyDown={key}
        />
        {box && (
          <div
            className="pointer-events-none absolute border border-sky-400"
            style={{
              left: percent(box.x, width),
              top: percent(box.y, height),
              width: percent(box.width, width),
              height: percent(box.height, height)
            }}
          />
        )}
      </div>
    </div>
  );
};

export const OledEditor = ({ open, onClose, displayOff }: Props) => {
  const { t } = useTranslation();
  const [builtIn, setBuiltIn] = useState<OledLayout | null>(null);
  const [layout, setLayout] = useState<OledLayout | null>(null);
  const [selected, setSelected] = useState(-1);
  const [values, setValues] = useState<OledValues>(sampleValues);
  const [live, setLive] = useState(false);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState(false);
  const pictureInput = useRef<HTMLInputElement>(null);
  const importInput = useRef<HTMLInputElement>(null);
  const pictureTarget = useRef<number>(-1);

  useEffect(() => {
    if (!open) return;
    let disposed = false;
    setLayout(null);
    setSelected(-1);
    setLive(false);
    setLoadError(false);
    api
      .getOLEDLayout()
      .then((rsp) => {
        if (disposed) return;
        if (rsp.code !== 0 || !rsp.data.builtIn) {
          setLoadError(true);
          return;
        }
        setBuiltIn(rsp.data.builtIn);
        setLayout(copy(rsp.data.saved ?? rsp.data.builtIn));
      })
      .catch(() => !disposed && setLoadError(true));
    return () => {
      disposed = true;
    };
  }, [open]);

  // The panel shows the preview while it is sent again in time.
  useEffect(() => {
    if (!open || !live || !layout) return;
    const send = () => api.setOLEDPreview(layout).catch(() => undefined);
    const first = window.setTimeout(send, 300);
    const again = window.setInterval(send, PREVIEW_REFRESH_MS);
    return () => {
      window.clearTimeout(first);
      window.clearInterval(again);
    };
  }, [open, live, layout]);

  useEffect(() => {
    if (open && live) return () => void api.deleteOLEDPreview().catch(() => undefined);
  }, [open, live]);

  if (!open) return null;

  const size = layout ? logicalSize(layout) : { width: 128, height: 64 };
  const item = layout && selected >= 0 ? layout.items[selected] : undefined;
  const pages = layout?.pages && layout.pages > 1 ? layout.pages : 1;
  const itemWhen = splitWhen(item?.when);

  const change = (next: OledLayout) => setLayout(next);
  const changeItem = (patch: Partial<OledItem>) => {
    if (!layout || selected < 0) return;
    const items = layout.items.slice();
    items[selected] = { ...items[selected], ...patch };
    change({ ...layout, items });
  };
  // A new item goes on the page shown; a duplicate keeps its original's.
  const add = (next: OledItem, duplicate = false) => {
    if (!layout) return;
    if (layout.items.length >= maxItems) {
      message.warning(t(`${KEY}.tooMany`, { count: maxItems }));
      return;
    }
    const when =
      pages > 1 && !duplicate ? joinWhen(splitWhen(next.when).states, values.page + 1) : next.when;
    change({ ...layout, items: [...layout.items, { ...next, when }] });
    setSelected(layout.items.length);
  };
  const reorder = (from: number, to: number) => {
    if (!layout || to < 0 || to >= layout.items.length) return;
    const items = layout.items.slice();
    const [moving] = items.splice(from, 1);
    items.splice(to, 0, moving);
    change({ ...layout, items });
    setSelected(to);
  };
  const remove = (index: number) => {
    if (!layout) return;
    change({ ...layout, items: layout.items.filter((_, i) => i !== index) });
    setSelected(-1);
  };

  const choosePicture = (index: number) => {
    pictureTarget.current = index;
    pictureInput.current?.click();
  };
  const loadPicture = async (file: File | undefined) => {
    if (!file || !layout) return;
    try {
      const picture = await pictureItem(file, size.width, size.height);
      if (pictureTarget.current >= 0) {
        const current = layout.items[pictureTarget.current];
        const items = layout.items.slice();
        items[pictureTarget.current] = {
          ...picture,
          x: current.x,
          y: current.y,
          when: current.when,
          off: current.off
        };
        change({ ...layout, items });
      } else {
        add(picture);
      }
    } catch {
      message.error(t(`${KEY}.pictureFailed`));
    }
  };

  const exportLayout = () => {
    if (!layout) return;
    const url = URL.createObjectURL(
      new Blob([JSON.stringify(layout, null, 2)], { type: 'application/json' })
    );
    const link = document.createElement('a');
    link.href = url;
    link.download = `oled-layout-${layout.panel}.json`;
    link.click();
    URL.revokeObjectURL(url);
  };
  const importLayout = async (file: File | undefined) => {
    if (!file || !layout) return;
    try {
      const parsed = JSON.parse(await file.text()) as OledLayout;
      if (parsed.panel !== layout.panel || !Array.isArray(parsed.items)) throw new Error('panel');
      change(parsed);
      setSelected(-1);
    } catch {
      message.error(t(`${KEY}.importFailed`));
    }
  };

  const save = async () => {
    if (!layout) return;
    setSaving(true);
    try {
      const rsp = await api.setOLEDLayout(layout);
      if (rsp.code !== 0) {
        showRequestError(rsp, `${KEY}.saveFailed`);
        return;
      }
      message.success(t(`${KEY}.saved`));
      onClose();
    } catch (err) {
      showRequestError(err, `${KEY}.saveFailed`);
    } finally {
      setSaving(false);
    }
  };
  const reset = async () => {
    try {
      const rsp = await api.deleteOLEDLayout();
      if (rsp.code !== 0) {
        showRequestError(rsp, `${KEY}.saveFailed`);
        return;
      }
      if (builtIn) change(copy(builtIn));
      setSelected(-1);
      message.success(t(`${KEY}.resetDone`));
    } catch (err) {
      showRequestError(err, `${KEY}.saveFailed`);
    }
  };

  // A state and its negation exclude each other.
  const whenOptions = conditions.flatMap((name) =>
    [name, `!${name}`].map((value) => ({
      value,
      label: t(value === name ? `${KEY}.when` : `${KEY}.whenNot`, {
        state: t(`${KEY}.state.${name}`)
      }),
      disabled: itemWhen.states.includes(value.startsWith('!') ? name : `!${name}`)
    }))
  );
  const pageLabel = (page: number) => t(`${KEY}.pageN`, { page });
  const label = (entry: OledItem) =>
    entry.type === 'text' ? entry.text || t(`${KEY}.emptyText`) : t(`${KEY}.type.${entry.type}`);
  const typeIcon = { text: TypeIcon, line: MinusIcon, rect: SquareIcon, image: ImageIcon };

  const number = (name: keyof OledItem, min = -128, max = 255) => (
    <label className="text-fg-muted flex flex-col gap-1 text-xs">
      {t(`${KEY}.field.${name}`)}
      <InputNumber
        size="small"
        min={min}
        max={max}
        value={(item?.[name] as number | undefined) ?? 0}
        onChange={(value) => changeItem({ [name]: Math.round(Number(value ?? 0)) })}
      />
    </label>
  );

  return (
    <Modal
      title={t(`${KEY}.title`)}
      open={open}
      width={980}
      onCancel={onClose}
      destroyOnHidden
      footer={
        layout && (
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-2">
              <Button onClick={() => importInput.current?.click()}>{t(`${KEY}.import`)}</Button>
              <Button onClick={exportLayout}>{t(`${KEY}.export`)}</Button>
              <Popconfirm title={t(`${KEY}.resetConfirm`)} onConfirm={reset}>
                <Button>{t(`${KEY}.reset`)}</Button>
              </Popconfirm>
            </div>
            <div className="flex items-center gap-2">
              <Button onClick={onClose}>{t(`${KEY}.cancel`)}</Button>
              <Button type="primary" loading={saving} onClick={save}>
                {t(`${KEY}.save`)}
              </Button>
            </div>
          </div>
        )
      }
    >
      <input
        ref={pictureInput}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={(event) => {
          void loadPicture(event.target.files?.[0]);
          event.target.value = '';
        }}
      />
      <input
        ref={importInput}
        type="file"
        accept="application/json,.json"
        className="hidden"
        onChange={(event) => {
          void importLayout(event.target.files?.[0]);
          event.target.value = '';
        }}
      />
      {loadError && <Alert type="error" showIcon message={t(`${KEY}.loadFailed`)} />}
      {!layout && !loadError && (
        <div className="flex justify-center py-10">
          <Spin />
        </div>
      )}
      {layout && (
        <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_280px]">
          <div className="space-y-3">
            <Preview
              layout={layout}
              values={values}
              selected={selected}
              onSelect={setSelected}
              onMove={(dx, dy) => item && changeItem(moved(item, dx, dy))}
            />
            <div className="text-fg-muted text-xs">
              {layout.burnin === 'roam' && `${t(`${KEY}.roamHint`)} `}
              {t(`${KEY}.hint`, { width: size.width, height: size.height })}
            </div>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
              <span className="text-fg-muted text-xs">{t(`${KEY}.previewState`)}</span>
              {conditions.map((name) => (
                <Checkbox
                  key={name}
                  checked={values[name]}
                  onChange={(event) => setValues({ ...values, [name]: event.target.checked })}
                >
                  {t(`${KEY}.state.${name}`)}
                </Checkbox>
              ))}
              {pages > 1 && (
                <Segmented
                  size="small"
                  value={values.page + 1}
                  options={Array.from({ length: pages }, (_, i) => ({
                    value: i + 1,
                    label: pageLabel(i + 1)
                  }))}
                  onChange={(page) => setValues({ ...values, page: Number(page) - 1 })}
                />
              )}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Switch id="oled-live" size="small" checked={live} onChange={setLive} />
              <label htmlFor="oled-live" className="text-sm">
                {t(`${KEY}.live`)}
              </label>
            </div>
            {live && displayOff && (
              <Alert type="warning" showIcon message={t(`${KEY}.displayOff`)} />
            )}
            <div className="grid grid-cols-2 gap-3">
              <label className="text-fg-muted flex flex-col gap-1 text-xs">
                {t(`${KEY}.rotation`)}
                <Select
                  size="small"
                  value={layout.rotation}
                  options={[0, 90, 180, 270].map((value) => ({ value, label: `${value}°` }))}
                  onChange={(rotation) => change({ ...layout, rotation })}
                />
              </label>
              <label className="text-fg-muted flex flex-col gap-1 text-xs">
                {t(`${KEY}.burnin`)}
                <Select
                  size="small"
                  value={layout.burnin}
                  options={(['none', 'shift', 'roam'] as const).map((value) => ({
                    value,
                    label: t(`${KEY}.burninMode.${value}`)
                  }))}
                  onChange={(burnin) =>
                    change({
                      ...layout,
                      burnin,
                      roamWidth: layout.roamWidth ?? size.width - 8,
                      roamHeight: layout.roamHeight ?? size.height / 2
                    })
                  }
                />
              </label>
              <label className="text-fg-muted flex flex-col gap-1 text-xs">
                {t(`${KEY}.pages`)}
                <Select
                  size="small"
                  value={pages}
                  options={Array.from({ length: maxPages }, (_, i) => ({
                    value: i + 1,
                    label: String(i + 1)
                  }))}
                  onChange={(count) => {
                    change({
                      ...layout,
                      pages: count > 1 ? count : undefined,
                      pageSeconds: count > 1 ? (layout.pageSeconds ?? 5) : undefined
                    });
                    if (values.page >= count) setValues({ ...values, page: 0 });
                  }}
                />
              </label>
              {pages > 1 && (
                <label className="text-fg-muted flex flex-col gap-1 text-xs">
                  {t(`${KEY}.pageSeconds`)}
                  <InputNumber
                    size="small"
                    min={1}
                    max={60}
                    value={layout.pageSeconds ?? 5}
                    onChange={(value) =>
                      change({ ...layout, pageSeconds: Math.round(Number(value ?? 5)) })
                    }
                  />
                </label>
              )}
              {layout.burnin === 'roam' &&
                (['roamWidth', 'roamHeight'] as const).map((name) => (
                  <label key={name} className="text-fg-muted flex flex-col gap-1 text-xs">
                    {t(`${KEY}.field.${name}`)}
                    <InputNumber
                      size="small"
                      min={1}
                      max={128}
                      value={layout[name]}
                      onChange={(value) =>
                        change({ ...layout, [name]: Math.round(Number(value ?? 1)) })
                      }
                    />
                  </label>
                ))}
            </div>
          </div>

          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium">{t(`${KEY}.items`)}</span>
              <Dropdown
                trigger={['click']}
                menu={{
                  items: [
                    { key: 'text', label: t(`${KEY}.type.text`) },
                    { key: 'line', label: t(`${KEY}.type.line`) },
                    { key: 'rect', label: t(`${KEY}.type.rect`) },
                    { key: 'picture', label: t(`${KEY}.picture`) },
                    {
                      key: 'icons',
                      label: t(`${KEY}.icons`),
                      children: icons.map((icon, index) => ({
                        key: `icon-${index}`,
                        label: `${icon.name} ${t(icon.on ? `${KEY}.iconOn` : `${KEY}.iconOff`)}`
                      }))
                    }
                  ],
                  onClick: ({ key }) => {
                    if (key === 'text')
                      add({
                        type: 'text',
                        x: 0,
                        y: 0,
                        font: 8,
                        align: 'left',
                        width: 0,
                        text: '{ip}'
                      });
                    else if (key === 'line')
                      add({ type: 'line', x: 0, y: 0, x2: size.width - 1, y2: 0 });
                    else if (key === 'rect')
                      add({ type: 'rect', x: 0, y: 0, width: 16, height: 8 });
                    else if (key === 'picture') choosePicture(-1);
                    else if (key.startsWith('icon-')) {
                      const icon = icons[Number(key.slice(5))];
                      add({
                        type: 'image',
                        x: 0,
                        y: 0,
                        width: icon.width,
                        height: 7,
                        data: icon.data
                      });
                    }
                  }
                }}
              >
                <Button size="small" icon={<PlusIcon size={14} />}>
                  {t(`${KEY}.add`)}
                </Button>
              </Dropdown>
            </div>
            <ul className="m-0 max-h-[50vh] list-none space-y-1 overflow-y-auto p-0">
              {layout.items.map((entry, index) => {
                const Icon = typeIcon[entry.type];
                return (
                  <li
                    key={index}
                    ref={(element) => {
                      if (element && index === selected)
                        element.scrollIntoView({ block: 'nearest' });
                    }}
                    className={clsx(
                      'flex cursor-pointer items-center gap-2 rounded px-2 py-1 text-sm',
                      index === selected ? 'bg-surface-raised text-fg' : 'text-fg-muted',
                      // Not drawn in the state previewed (another page, say).
                      !holds(entry, values) && 'opacity-50'
                    )}
                    onClick={() => setSelected(index)}
                  >
                    <Icon size={14} className="shrink-0" />
                    <span className="min-w-0 flex-1 truncate font-mono">{label(entry)}</span>
                    {entry.when && <span className="text-xs">{entry.when}</span>}
                  </li>
                );
              })}
            </ul>

            {item && (
              <div className="border-border space-y-3 border-t pt-3">
                <div className="flex gap-1">
                  <Button
                    size="small"
                    icon={<ArrowUpIcon size={14} />}
                    aria-label={t(`${KEY}.up`)}
                    onClick={() => reorder(selected, selected - 1)}
                  />
                  <Button
                    size="small"
                    icon={<ArrowDownIcon size={14} />}
                    aria-label={t(`${KEY}.down`)}
                    onClick={() => reorder(selected, selected + 1)}
                  />
                  <Button
                    size="small"
                    icon={<CopyIcon size={14} />}
                    aria-label={t(`${KEY}.duplicate`)}
                    onClick={() => add(moved(item, 2, 2), true)}
                  />
                  <Button
                    size="small"
                    danger
                    icon={<Trash2Icon size={14} />}
                    aria-label={t(`${KEY}.remove`)}
                    onClick={() => remove(selected)}
                  />
                </div>
                {item.type === 'text' && (
                  <>
                    <label className="text-fg-muted flex flex-col gap-1 text-xs">
                      {t(`${KEY}.field.text`)}
                      <Input
                        size="small"
                        className="font-mono"
                        maxLength={maxText}
                        value={item.text}
                        onChange={(event) =>
                          changeItem({ text: event.target.value.replace(/[^\x20-\x7e]/g, '') })
                        }
                      />
                    </label>
                    <div className="text-fg-muted text-xs">
                      {t(`${KEY}.variables`)} {variables.map((name) => `{${name}}`).join(' ')}
                    </div>
                  </>
                )}
                <div className="grid grid-cols-2 gap-2">
                  {number('x')}
                  {number('y')}
                  {item.type === 'line' && number('x2')}
                  {item.type === 'line' && number('y2')}
                  {(item.type === 'rect' || item.type === 'image') && number('width', 1, 128)}
                  {(item.type === 'rect' || item.type === 'image') && number('height', 1, 128)}
                  {item.type === 'text' && number('width', 0, 128)}
                </div>
                {item.type === 'text' && (
                  <div className="grid grid-cols-2 gap-2">
                    <label className="text-fg-muted flex flex-col gap-1 text-xs">
                      {t(`${KEY}.field.font`)}
                      <Select
                        size="small"
                        value={item.font ?? 8}
                        options={[
                          { value: 4, label: t(`${KEY}.font.4`) },
                          { value: 8, label: t(`${KEY}.font.8`) },
                          { value: 16, label: t(`${KEY}.font.16`) }
                        ]}
                        onChange={(font) => changeItem({ font })}
                      />
                    </label>
                    <label className="text-fg-muted flex flex-col gap-1 text-xs">
                      {t(`${KEY}.field.align`)}
                      <Select
                        size="small"
                        value={item.align ?? 'left'}
                        disabled={!item.width}
                        options={(['left', 'center', 'right'] as const).map((value) => ({
                          value,
                          label: t(`${KEY}.align.${value}`)
                        }))}
                        onChange={(align) => changeItem({ align })}
                      />
                    </label>
                  </div>
                )}
                {item.type === 'image' &&
                  item.data &&
                  item.data.length !==
                    Math.ceil((item.width ?? 1) / 8) * (item.height ?? 1) * 2 && (
                    <Alert type="warning" showIcon message={t(`${KEY}.imageSize`)} />
                  )}
                {item.type === 'image' && (
                  <div className="flex flex-wrap gap-2">
                    <Button size="small" onClick={() => choosePicture(selected)}>
                      {t(`${KEY}.replacePicture`)}
                    </Button>
                    <Button size="small" onClick={() => changeItem({ data: inverted(item) })}>
                      {t(`${KEY}.invert`)}
                    </Button>
                  </div>
                )}
                <label className="text-fg-muted flex flex-col gap-1 text-xs">
                  {t(`${KEY}.field.when`)}
                  <Select
                    size="small"
                    mode="multiple"
                    placeholder={t(`${KEY}.always`)}
                    value={itemWhen.states}
                    options={whenOptions}
                    maxCount={maxConditions - (itemWhen.page ? 1 : 0)}
                    onChange={(states: string[]) =>
                      changeItem({ when: joinWhen(states, itemWhen.page) })
                    }
                  />
                </label>
                {(pages > 1 || itemWhen.page > 0) && (
                  <label className="text-fg-muted flex flex-col gap-1 text-xs">
                    {t(`${KEY}.field.page`)}
                    <Select
                      size="small"
                      value={itemWhen.page}
                      options={[
                        { value: 0, label: t(`${KEY}.allPages`) },
                        ...Array.from({ length: maxPages }, (_, i) => ({
                          value: i + 1,
                          label: pageLabel(i + 1),
                          disabled: i + 1 > pages
                        }))
                      ]}
                      onChange={(page) => {
                        // The page counts as one of the conditions.
                        const states = page
                          ? itemWhen.states.slice(0, maxConditions - 1)
                          : itemWhen.states;
                        changeItem({ when: joinWhen(states, page) });
                      }}
                    />
                  </label>
                )}
                <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
                  {item.type === 'rect' && (
                    <Checkbox
                      checked={!!item.fill}
                      onChange={(event) => changeItem({ fill: event.target.checked })}
                    >
                      {t(`${KEY}.field.fill`)}
                    </Checkbox>
                  )}
                  <Checkbox
                    checked={!!item.off}
                    onChange={(event) => changeItem({ off: event.target.checked })}
                  >
                    {t(`${KEY}.field.off`)}
                  </Checkbox>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </Modal>
  );
};
