export type TimePreferences = { timezone: string; format: '24' | '12' };
export type DateTimeConfig = TimePreferences & { servers: string[] };

export const defaultTimePreferences: TimePreferences = { timezone: 'UTC', format: '24' };

export function formatDeviceTime(
  value: number,
  preferences: TimePreferences,
  locale?: string,
  date = false
) {
  return new Intl.DateTimeFormat(locale, {
    timeZone: preferences.timezone,
    hourCycle: preferences.format === '12' ? 'h12' : 'h23',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    ...(date ? { year: 'numeric' as const, month: 'short' as const, day: 'numeric' as const } : {})
  }).format(value);
}
