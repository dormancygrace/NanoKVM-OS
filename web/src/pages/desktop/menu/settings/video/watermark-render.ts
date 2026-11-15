// The watermark picture: composed in the browser at reference scale (a frame
// refWidth pixels wide), sent as a PNG and drawn by the device into every
// stream. The preview uses the same picture and the device's placement.

export type WatermarkBackground = 'none' | 'dark' | 'light';

// The editor's record, saved as the watermark's design (without the logo
// itself: hasLogo only says the saved picture contains one).
export type WatermarkDesign = {
  text: string;
  fontSize: number;
  bold: boolean;
  color: string;
  background: WatermarkBackground;
  opacity: number;
  hasLogo: boolean;
};

export const FONT_SIZE = { min: 16, max: 96 };
export const OPACITY = { min: 10, max: 100 };
export const MARGIN = { min: 0, max: 200 };
export const MAX_LINES = 3;
export const MAX_LINE_LENGTH = 60;

export const DEFAULT_DESIGN: WatermarkDesign = {
  text: '',
  fontSize: 32,
  bold: false,
  color: '#FFFFFF',
  background: 'dark',
  opacity: 80,
  hasLogo: false
};

const BACKGROUNDS: Record<Exclude<WatermarkBackground, 'none'>, string> = {
  dark: 'rgba(0, 0, 0, 0.55)',
  light: 'rgba(255, 255, 255, 0.65)'
};
const FONT_FAMILY =
  'system-ui, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif';

const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

// At most MAX_LINES lines of MAX_LINE_LENGTH characters.
export function limitText(text: string) {
  return text
    .replace(/\r\n?/g, '\n')
    .split('\n')
    .slice(0, MAX_LINES)
    .map((line) => line.slice(0, MAX_LINE_LENGTH))
    .join('\n');
}

// The saved design ({blocks: [...]}, one entry per block): the blocks'
// designs, count entries long. Anything missing, mistyped or out of range
// keeps its default.
export function parseDesigns(raw: string, count: number): WatermarkDesign[] {
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    value = undefined;
  }
  const blocks =
    value && typeof value === 'object' && Array.isArray((value as { blocks?: unknown }).blocks)
      ? ((value as { blocks: unknown[] }).blocks as unknown[])
      : [];
  return Array.from({ length: count }, (_, i) => parseBlockDesign(blocks[i]));
}

// The design JSON the server keeps: at most DESIGN_MAX_BYTES of UTF-8.
export const DESIGN_MAX_BYTES = 8192;
export const designJson = (designs: WatermarkDesign[]) => JSON.stringify({ blocks: designs });

function parseBlockDesign(value: unknown): WatermarkDesign {
  if (!value || typeof value !== 'object') return DEFAULT_DESIGN;
  const v = value as Record<string, unknown>;
  const number = (x: unknown, min: number, max: number, fallback: number) =>
    typeof x === 'number' && Number.isFinite(x) ? clamp(Math.round(x), min, max) : fallback;
  return {
    text: typeof v.text === 'string' ? limitText(v.text) : DEFAULT_DESIGN.text,
    fontSize: number(v.fontSize, FONT_SIZE.min, FONT_SIZE.max, DEFAULT_DESIGN.fontSize),
    bold: typeof v.bold === 'boolean' ? v.bold : DEFAULT_DESIGN.bold,
    color:
      typeof v.color === 'string' && /^#[0-9a-fA-F]{6}$/.test(v.color)
        ? v.color.toUpperCase()
        : DEFAULT_DESIGN.color,
    background:
      v.background === 'none' || v.background === 'dark' || v.background === 'light'
        ? v.background
        : DEFAULT_DESIGN.background,
    opacity: number(v.opacity, OPACITY.min, OPACITY.max, DEFAULT_DESIGN.opacity),
    hasLogo: v.hasLogo === true
  };
}

// The fields that make up the picture (the logo is compared separately).
export function pictureKey(design: WatermarkDesign) {
  const { text, fontSize, bold, color, background, opacity } = design;
  return JSON.stringify([text, fontSize, bold, color, background, opacity]);
}

const textLines = (text: string) => {
  const lines = text.split('\n').map((line) => line.trimEnd());
  while (lines.length && !lines[lines.length - 1].trim()) lines.pop();
  return lines;
};

const evenUpTo = (value: number, max: number) =>
  Math.max(2, Math.min(max - (max % 2), Math.ceil(value / 2) * 2));

/*
 * The picture in reference pixels: the background with rounded corners, the
 * logo at the text block's height on the left, then the text lines. Overall
 * opacity applies to the finished picture, so text over the background does
 * not blend twice. Larger than maxWidth x maxHeight, it is scaled down to fit
 * (scaled). The size is even, as the device places and scales even sizes.
 * null: nothing to draw.
 */
export function renderWatermark(
  design: WatermarkDesign,
  logo: HTMLImageElement | null,
  maxWidth: number,
  maxHeight: number
): { canvas: HTMLCanvasElement; scaled: boolean } | null {
  const lines = textLines(design.text);
  const hasText = lines.some((line) => line.trim());
  if (!hasText && !logo) return null;

  const fontSize = design.fontSize;
  const font = `${design.bold ? 'bold ' : ''}${fontSize}px ${FONT_FAMILY}`;
  const lineHeight = Math.round(fontSize * 1.25);
  // Without a background, only room for glyphs that reach past their box.
  const pad = Math.round(fontSize * (design.background === 'none' ? 0.1 : 0.4));

  const measure = document.createElement('canvas').getContext('2d');
  if (!measure) return null;
  measure.font = font;
  const textWidth = hasText ? Math.max(...lines.map((line) => measure.measureText(line).width)) : 0;
  const textHeight = hasText ? lines.length * lineHeight : 0;

  const logoHeight = logo ? (hasText ? textHeight : lineHeight * 2) : 0;
  const aspect = logo && logo.naturalHeight ? logo.naturalWidth / logo.naturalHeight : 1;
  const logoWidth = logo ? logoHeight * aspect : 0;
  const gap = logo && hasText ? Math.round(fontSize * 0.4) : 0;

  const width = pad * 2 + logoWidth + gap + textWidth;
  const height = pad * 2 + Math.max(textHeight, logoHeight);
  const scale = Math.min(1, maxWidth / width, maxHeight / height);
  const outWidth = evenUpTo(width * scale, maxWidth);
  const outHeight = evenUpTo(height * scale, maxHeight);
  // The drawing area in reference pixels: the output size before scaling.
  const areaWidth = outWidth / scale;
  const areaHeight = outHeight / scale;

  const layer = document.createElement('canvas');
  layer.width = outWidth;
  layer.height = outHeight;
  const ctx = layer.getContext('2d');
  if (!ctx) return null;
  ctx.scale(scale, scale);
  if (design.background !== 'none') {
    ctx.fillStyle = BACKGROUNDS[design.background];
    ctx.beginPath();
    ctx.roundRect(0, 0, areaWidth, areaHeight, Math.min(pad, areaHeight / 2));
    ctx.fill();
  }
  const left = (areaWidth - (logoWidth + gap + textWidth)) / 2;
  if (logo) {
    ctx.imageSmoothingQuality = 'high';
    ctx.drawImage(logo, left, (areaHeight - logoHeight) / 2, logoWidth, logoHeight);
  }
  if (hasText) {
    ctx.font = font;
    ctx.fillStyle = design.color;
    ctx.textAlign = 'left';
    ctx.textBaseline = 'middle';
    const top = (areaHeight - textHeight) / 2;
    lines.forEach((line, i) =>
      ctx.fillText(line, left + logoWidth + gap, top + i * lineHeight + lineHeight / 2)
    );
  }

  const canvas = document.createElement('canvas');
  canvas.width = outWidth;
  canvas.height = outHeight;
  const out = canvas.getContext('2d');
  if (!out) return null;
  out.globalAlpha = design.opacity / 100;
  out.drawImage(layer, 0, 0);
  return { canvas, scaled: scale < 1 };
}

// Where the device puts a block of size (one axis) in a frame of length
// (kvm_mmf.cpp, _wm_attach_block_locked): permille of the free room inside
// the margin, kept inside the frame.
export function placeBlock(length: number, size: number, permille: number, margin: number) {
  const room = Math.max(0, length - size - 2 * margin);
  return clamp(margin + (room * permille) / 1000, 0, Math.max(0, length - size));
}

// The permille that puts a block's near edge at offset, snapped to the edges
// and the centre within SNAP permille.
const SNAP = 30;
export function permilleAt(length: number, size: number, offset: number, margin: number) {
  const room = length - size - 2 * margin;
  if (room <= 0) return 500;
  const value = clamp(Math.round(((offset - margin) / room) * 1000), 0, 1000);
  const snap = [0, 500, 1000].find((point) => Math.abs(point - value) <= SNAP);
  return snap ?? value;
}

type Rect = { x: number; y: number; width: number; height: number };

// The pairs of blocks (indexes) whose rectangles overlap.
export function overlaps(rects: (Rect | undefined)[]) {
  const pairs: [number, number][] = [];
  rects.forEach((a, i) =>
    rects.slice(i + 1).forEach((b, k) => {
      if (
        a &&
        b &&
        a.x < b.x + b.width &&
        b.x < a.x + a.width &&
        a.y < b.y + b.height &&
        b.y < a.y + a.height
      )
        pairs.push([i, i + 1 + k]);
    })
  );
  return pairs;
}

// The size of a base64 PNG, read from its header; undefined if it is none.
export function pngSize(base64: string) {
  try {
    const head = atob(base64.slice(0, 32));
    if (head.slice(1, 4) !== 'PNG' || head.length < 24) return undefined;
    const word = (at: number) =>
      ((head.charCodeAt(at) << 24) |
        (head.charCodeAt(at + 1) << 16) |
        (head.charCodeAt(at + 2) << 8) |
        head.charCodeAt(at + 3)) >>>
      0;
    return { width: word(16), height: word(20) };
  } catch {
    return undefined;
  }
}

// The PNG, base64 without the data: prefix.
export function canvasPngBase64(canvas: HTMLCanvasElement): Promise<string> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (!blob) {
        reject(new Error('no picture'));
        return;
      }
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result).split(',')[1] ?? '');
      reader.onerror = () => reject(reader.error ?? new Error('no picture'));
      reader.readAsDataURL(blob);
    }, 'image/png');
  });
}

// The picture can be exported from a canvas: some SVGs (foreignObject) taint
// it, and the PNG could not be made.
function exportable(image: HTMLImageElement) {
  const canvas = document.createElement('canvas');
  canvas.width = canvas.height = 2;
  try {
    canvas.getContext('2d')?.drawImage(image, 0, 0, 2, 2);
    canvas.toDataURL();
    return true;
  } catch {
    return false;
  }
}

// A picture file read in the browser. Rejects a file that is no picture,
// has no size of its own (like some SVGs) or cannot be exported.
export function loadLogo(file: File): Promise<HTMLImageElement> {
  return new Promise((resolve, reject) => {
    const url = URL.createObjectURL(file);
    const image = new Image();
    image.onload = () => {
      URL.revokeObjectURL(url);
      if (image.naturalWidth && image.naturalHeight && exportable(image)) resolve(image);
      else reject(new Error('unusable picture'));
    };
    image.onerror = () => {
      URL.revokeObjectURL(url);
      reject(new Error('no picture'));
    };
    image.src = url;
  });
}
