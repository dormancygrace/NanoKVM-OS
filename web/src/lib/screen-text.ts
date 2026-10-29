// Text read from the screen by the TPU add-on (GET /api/vm/screen/text):
// boxes of words in the pixels of the captured picture.

export type ScreenTextBox = {
  text: string;
  score: number;
  x: number;
  y: number;
  w: number;
  h: number;
};

export type ScreenTextResult = {
  width: number;
  height: number;
  boxes: ScreenTextBox[];
  total_ms: number;
};

// The recognizers of the add-on; the first is the server's default.
export const screenTextLanguages = ['latin', 'chinese', 'cyrillic'] as const;
export type ScreenTextLanguage = (typeof screenTextLanguages)[number];

export function isScreenTextLanguage(value: unknown): value is ScreenTextLanguage {
  return screenTextLanguages.includes(value as ScreenTextLanguage);
}

// The switch of Settings > AI > Text recognition from its GET reply. A server
// from before the switch answers {language, languages}: there text
// recognition is always on and cannot be turned off.
export function screenTextSwitch(data: { enabled?: unknown } | null | undefined) {
  if (typeof data?.enabled === 'boolean') return { enabled: data.enabled, switchable: true };
  return { enabled: true, switchable: false };
}

// The reading dialog keeps the last language picked in this browser; a first
// read is in Latin.
const languageKey = 'nano-kvm-screen-text-language';

export function storedScreenTextLanguage(): ScreenTextLanguage {
  try {
    const stored = globalThis.localStorage?.getItem(languageKey);
    if (isScreenTextLanguage(stored)) return stored;
  } catch {
    // Storage blocked: the default.
  }
  return screenTextLanguages[0];
}

export function storeScreenTextLanguage(language: ScreenTextLanguage) {
  try {
    globalThis.localStorage?.setItem(languageKey, language);
  } catch {
    // Storage blocked: the pick lasts until the page reloads.
  }
}

// A part of the picture as fractions of its width and height (0..1).
export type ScreenTextRegion = { left: number; top: number; width: number; height: number };

// The boxes whose centre lies in the region.
export function boxesInRegion(
  result: Pick<ScreenTextResult, 'width' | 'height' | 'boxes'>,
  region: ScreenTextRegion
) {
  return result.boxes.filter((box) => {
    const x = (box.x + box.w / 2) / result.width;
    const y = (box.y + box.h / 2) / result.height;
    return (
      x >= region.left &&
      x <= region.left + region.width &&
      y >= region.top &&
      y <= region.top + region.height
    );
  });
}

const centre = (box: ScreenTextBox) => box.y + box.h / 2;

// b continues a on the same line: it starts past the middle of a, and their
// centres are within half the lower box's height. Neighbours only: lines on
// photos are slanted, and lines of small text are closer together than the
// boxes are high, so a line's far ends need not overlap.
function follows(a: ScreenTextBox, b: ScreenTextBox) {
  return b.x >= a.x + a.w / 2 && Math.abs(centre(a) - centre(b)) <= Math.min(a.h, b.h) / 2;
}

// Plain text in reading order. The server gives the pieces of a detected line
// one after another, left to right: those chain into runs; runs side by side
// (a row of a table, a line the detector cut) join into one line; lines go
// top to bottom by their first box.
export function boxesToText(boxes: ScreenTextBox[]) {
  const runs: ScreenTextBox[][] = [];
  for (const box of boxes) {
    const run = runs[runs.length - 1];
    if (run && follows(run[run.length - 1], box)) run.push(box);
    else runs.push([box]);
  }
  const lines: ScreenTextBox[][] = [];
  for (const run of runs.sort((a, b) => centre(a[0]) - centre(b[0]))) {
    const after = lines.find((line) => follows(line[line.length - 1], run[0]));
    const before = after ? undefined : lines.find((line) => follows(run[run.length - 1], line[0]));
    if (after) after.push(...run);
    else if (before) before.unshift(...run);
    else lines.push(run);
  }
  return lines
    .sort((a, b) => centre(a[0]) - centre(b[0]))
    .map((line) => line.map((box) => box.text).join(' '))
    .join('\n');
}
