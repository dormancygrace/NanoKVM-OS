export type TransferProgress = {
  percent: number | null;
  bytes: number | null;
  total: number | null;
  speed: number | null;
};
const nonnegative = (value: unknown) =>
  typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
export function readTransferProgress(data: Record<string, unknown> = {}): TransferProgress {
  const bytes = nonnegative(data.downloadedBytes);
  const total = nonnegative(data.totalBytes);
  const legacy =
    typeof data.percentage === 'string' && /^\d+(\.\d+)?%?$/.test(data.percentage)
      ? Number.parseFloat(data.percentage)
      : null;
  const percent = bytes !== null && total !== null && total > 0 ? (bytes / total) * 100 : legacy;
  return {
    bytes,
    total,
    speed: nonnegative(data.bytesPerSecond),
    percent: percent === null ? null : Math.max(0, Math.min(100, percent))
  };
}
export function transferBytes(value: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB'];
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}
