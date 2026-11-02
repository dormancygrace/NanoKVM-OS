// The OLED main page layout, as the server and kvm_system's lib/oled_layout
// define it, and its drawing for the editor's preview. Drawing follows
// oled_layout.cpp: change both together.
import { fonts } from './font.ts';

export type OledPanel = 'wide' | 'narrow';
export type OledCondition = '' | 'eth' | 'wifi' | 'hdmi' | 'hid' | 'stream';
export type OledFont = 4 | 8 | 16;
export type OledAlign = 'left' | 'center' | 'right';

export type OledItem = {
  type: 'text' | 'line' | 'rect' | 'image';
  x: number;
  y: number;
  x2?: number;
  y2?: number;
  width?: number;
  height?: number;
  font?: OledFont;
  align?: OledAlign;
  fill?: boolean;
  off?: boolean;
  when?: string;
  text?: string;
  data?: string;
};

export type OledLayout = {
  panel: OledPanel;
  rotation: number;
  burnin: 'none' | 'shift' | 'roam';
  roamWidth?: number;
  roamHeight?: number;
  items: OledItem[];
};

export type OledValues = {
  ip: string;
  eth_ip: string;
  wifi_ip: string;
  hostname: string;
  codec: string;
  uptime: string;
  time: string;
  date: string;
  width: number;
  height: number;
  fps: number;
  eth: boolean;
  wifi: boolean;
  hdmi: boolean;
  hid: boolean;
  stream: boolean;
};

export const conditions: Exclude<OledCondition, ''>[] = ['eth', 'wifi', 'hdmi', 'hid', 'stream'];

export const variables = [
  'ip',
  'eth_ip',
  'wifi_ip',
  'hostname',
  'codec',
  'width',
  'height',
  'fps',
  'uptime',
  'time',
  'date'
] as const;

export const maxItems = 64;
export const maxText = 128;

export const panelSize = (panel: OledPanel) =>
  panel === 'narrow' ? { width: 64, height: 32 } : { width: 128, height: 64 };

// The size the layout is drawn in: the panel as rotated.
export function logicalSize(layout: OledLayout) {
  const { width, height } = panelSize(layout.panel);
  return layout.rotation % 180 ? { width: height, height: width } : { width, height };
}

export const advance = (font: OledFont) => (font === 4 ? 4 : font === 16 ? 8 : 6);

// {name} or {name:N}, character by character as oled_layout_expand does.
export function expand(text: string, values: OledValues): string {
  let out = '';
  for (let i = 0; i < text.length; i++) {
    const close = text[i] === '{' ? text.indexOf('}', i) : -1;
    if (close < 0) {
      out += text[i];
      continue;
    }
    let name = text.slice(i + 1, close);
    let pad = 0;
    const colon = name.indexOf(':');
    if (colon >= 0) {
      pad = parseInt(name.slice(colon + 1), 10) || 0;
      name = name.slice(0, colon);
    }
    if (!(variables as readonly string[]).includes(name)) {
      out += text[i];
      continue;
    }
    let value = String(values[name as keyof OledValues]);
    const wanted = Math.abs(pad);
    if (pad && value.length > wanted) value = value.slice(0, wanted);
    if (pad > 0) value = value.padStart(wanted);
    else if (pad < 0) value = value.padEnd(wanted);
    out += value;
    i = close;
  }
  return out;
}

function glyphColumn(chr: string, font: OledFont, column: number): number {
  let code = chr.charCodeAt(0);
  if (font === 4 && code >= 97 && code <= 122) code -= 32;
  const glyphs = fonts[font];
  let index = code - 32;
  if (index < 0 || index >= glyphs.length) index = 0;
  const glyph = glyphs[index];
  return font === 16 ? glyph[column] | (glyph[8 + column] << 8) : glyph[column];
}

export function holds(item: OledItem, values: OledValues): boolean {
  if (!item.when) return true;
  const negated = item.when.startsWith('!');
  const name = (negated ? item.when.slice(1) : item.when) as Exclude<OledCondition, ''>;
  return values[name] !== negated;
}

// The text as drawn: its position and size in pixels.
export function textBox(item: OledItem, values: OledValues) {
  const font = item.font ?? 8;
  const text = expand(item.text ?? '', values);
  const width = item.width || Math.max(text.length * advance(font), 1);
  return { x: item.x, y: item.y, width, height: font === 16 ? 16 : 8, text };
}

// What an item covers, for selection and dragging.
export function itemBox(item: OledItem, values: OledValues) {
  if (item.type === 'text') return textBox(item, values);
  if (item.type === 'line') {
    const x = Math.min(item.x, item.x2 ?? item.x);
    const y = Math.min(item.y, item.y2 ?? item.y);
    return {
      x,
      y,
      width: Math.abs((item.x2 ?? item.x) - item.x) + 1,
      height: Math.abs((item.y2 ?? item.y) - item.y) + 1
    };
  }
  return { x: item.x, y: item.y, width: item.width ?? 1, height: item.height ?? 1 };
}

// Draws the layout, unrotated and without the burn-in move: what the panel
// shows at rest, as the user sees it. Returns one entry per pixel.
export function render(
  layout: OledLayout,
  values: OledValues
): { width: number; height: number; pixels: Uint8Array } {
  const { width, height } = logicalSize(layout);
  const pixels = new Uint8Array(width * height);
  const set = (x: number, y: number, on: boolean) => {
    if (x < 0 || y < 0 || x >= width || y >= height) return;
    pixels[y * width + x] = on ? 1 : 0;
  };
  for (const item of layout.items) {
    if (!holds(item, values)) continue;
    const on = !item.off;
    if (item.type === 'text') {
      const font = item.font ?? 8;
      const step = advance(font);
      const rows = font === 16 ? 16 : 8;
      const text = expand(item.text ?? '', values);
      let x = item.x;
      const boxed = (item.width ?? 0) > 0;
      if (boxed) {
        const spare = item.width! - text.length * step;
        if (item.align === 'right') x += spare;
        else if (item.align === 'center') x += Math.trunc(spare / 2);
      }
      for (const chr of text) {
        for (let column = 0; column < step; column++) {
          const px = x + column;
          if (boxed && (px < item.x || px >= item.x + item.width!)) continue;
          const bits = glyphColumn(chr, font, column);
          for (let row = 0; row < rows; row++) if ((bits >> row) & 1) set(px, item.y + row, on);
        }
        x += step;
      }
    } else if (item.type === 'line') {
      let x = item.x;
      let y = item.y;
      const x2 = item.x2 ?? x;
      const y2 = item.y2 ?? y;
      const dx = Math.abs(x2 - x);
      const sx = x < x2 ? 1 : -1;
      const dy = -Math.abs(y2 - y);
      const sy = y < y2 ? 1 : -1;
      let error = dx + dy;
      for (;;) {
        set(x, y, on);
        if (x === x2 && y === y2) break;
        const twice = 2 * error;
        if (twice >= dy) {
          error += dy;
          x += sx;
        }
        if (twice <= dx) {
          error += dx;
          y += sy;
        }
      }
    } else if (item.type === 'rect') {
      const w = item.width ?? 1;
      const h = item.height ?? 1;
      for (let row = 0; row < h; row++)
        for (let column = 0; column < w; column++)
          if (item.fill || row === 0 || column === 0 || row === h - 1 || column === w - 1)
            set(item.x + column, item.y + row, on);
    } else {
      const w = item.width ?? 1;
      const h = item.height ?? 1;
      const stride = Math.ceil(w / 8);
      const data = item.data ?? '';
      for (let row = 0; row < h; row++)
        for (let column = 0; column < w; column++) {
          const offset = (row * stride + (column >> 3)) * 2;
          const byte = parseInt(data.slice(offset, offset + 2), 16) || 0;
          if ((byte >> (7 - (column & 7))) & 1) set(item.x + column, item.y + row, on);
        }
    }
  }
  return { width, height, pixels };
}

// A picture as an image item: scaled to fit within the size, a pixel is lit
// where it is light and not transparent.
export function imageFromPixels(
  rgba: Uint8ClampedArray,
  width: number,
  height: number,
  invert: boolean
): string {
  const stride = Math.ceil(width / 8);
  const bytes = new Uint8Array(stride * height);
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      const i = (y * width + x) * 4;
      const light = rgba[i] * 0.299 + rgba[i + 1] * 0.587 + rgba[i + 2] * 0.114 >= 128;
      const lit = rgba[i + 3] >= 128 && light !== invert;
      if (lit) bytes[y * stride + (x >> 3)] |= 0x80 >> (x & 7);
    }
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}

export const sampleValues: OledValues = {
  ip: '192.168.1.20',
  eth_ip: '192.168.1.20',
  wifi_ip: '192.168.4.128',
  hostname: 'kvm-1a2b',
  codec: 'H264',
  uptime: '3h05m',
  time: '12:34',
  date: '2026-11-02',
  width: 1920,
  height: 1080,
  fps: 30,
  eth: true,
  wifi: false,
  hdmi: true,
  hid: true,
  stream: true
};
