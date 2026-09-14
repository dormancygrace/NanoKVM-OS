export type ScreenshotSource = {
  width: number;
  height: number;
  capture: () => Promise<Blob>;
};

function canvasToPng(canvas: HTMLCanvasElement): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((blob) => {
      if (blob) resolve(blob);
      else reject(new Error('screenshot-encode-failed'));
    }, 'image/png');
  });
}

export async function captureMediaElement(element: HTMLVideoElement | HTMLImageElement) {
  const width = element instanceof HTMLVideoElement ? element.videoWidth : element.naturalWidth;
  const height = element instanceof HTMLVideoElement ? element.videoHeight : element.naturalHeight;
  const ready =
    element instanceof HTMLVideoElement
      ? element.readyState >= HTMLMediaElement.HAVE_CURRENT_DATA
      : element.complete;

  if (!ready || !width || !height) throw new Error('screenshot-no-video');

  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext('2d', { alpha: false });
  if (!context) throw new Error('screenshot-encode-failed');
  context.drawImage(element, 0, 0, width, height);
  return canvasToPng(canvas);
}

export function screenshotFilename(date = new Date()) {
  const pad = (value: number, length = 2) => String(value).padStart(length, '0');
  return `NanoKVM-screenshot-${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}_${pad(date.getHours())}-${pad(date.getMinutes())}-${pad(date.getSeconds())}-${pad(date.getMilliseconds(), 3)}.png`;
}

export function downloadScreenshot(blob: Blob, filename = screenshotFilename()) {
  const url = URL.createObjectURL(blob);
  const link = document.createElement('a');
  link.href = url;
  link.download = filename;
  link.style.display = 'none';
  document.body.appendChild(link);
  link.click();
  link.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}
