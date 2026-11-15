// The order of day, month and year; 'auto' follows the interface language.
export type DateFormat = 'auto' | 'dmy' | 'mdy' | 'ymd';
export type TimePreferences = { timezone: string; format: '24' | '12'; dateFormat?: DateFormat };
export type DateTimeConfig = TimePreferences & { servers: string[] };

export const defaultTimePreferences: TimePreferences = {
  timezone: 'UTC',
  format: '24',
  dateFormat: 'auto'
};

export function formatDeviceTime(
  value: number,
  preferences: TimePreferences,
  locale?: string,
  date = false
) {
  const timeOptions: Intl.DateTimeFormatOptions = {
    timeZone: preferences.timezone,
    hourCycle: preferences.format === '12' ? 'h12' : 'h23',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  };
  const dateFormat = preferences.dateFormat ?? 'auto';
  if (!date || dateFormat === 'auto') {
    return new Intl.DateTimeFormat(locale, {
      ...timeOptions,
      ...(date
        ? { year: 'numeric' as const, month: 'short' as const, day: 'numeric' as const }
        : {})
    }).format(value);
  }
  return `${formatDeviceDate(value, preferences.timezone, dateFormat)} ${new Intl.DateTimeFormat(locale, timeOptions).format(value)}`;
}

// A time to the minute, such as a planned run: weekday, date and time
// without seconds, in the device's time zone, 12/24-hour format and date order.
export function formatDeviceMinute(value: number, preferences: TimePreferences, locale?: string) {
  const timeZone = preferences.timezone;
  const time = new Intl.DateTimeFormat(locale, {
    timeZone,
    hourCycle: preferences.format === '12' ? 'h12' : 'h23',
    hour: '2-digit',
    minute: '2-digit'
  }).format(value);
  const weekday = new Intl.DateTimeFormat(locale, { timeZone, weekday: 'short' }).format(value);
  const dateFormat = preferences.dateFormat ?? 'auto';
  const date =
    dateFormat === 'auto'
      ? new Intl.DateTimeFormat(locale, { timeZone, month: 'short', day: 'numeric' }).format(value)
      : formatDeviceDate(value, timeZone, dateFormat);
  return `${weekday}, ${date} ${time}`;
}

// A clock time of day (no date, no time zone) in the 12/24-hour format.
export function formatClock(
  hour: number,
  minute: number,
  preferences: TimePreferences,
  locale?: string
) {
  return new Intl.DateTimeFormat(locale, {
    timeZone: 'UTC',
    hourCycle: preferences.format === '12' ? 'h12' : 'h23',
    hour: '2-digit',
    minute: '2-digit'
  }).format(Date.UTC(2026, 0, 1, hour, minute));
}

// A fixed order needs fixed digits: the parts come from a numeric format.
function formatDeviceDate(
  value: number,
  timeZone: string,
  dateFormat: Exclude<DateFormat, 'auto'>
) {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit'
  }).formatToParts(value);
  const part = (type: string) => parts.find((p) => p.type === type)?.value ?? '';
  const [year, month, day] = [part('year'), part('month'), part('day')];
  if (dateFormat === 'dmy') return `${day}.${month}.${year}`;
  if (dateFormat === 'mdy') return `${month}/${day}/${year}`;
  return `${year}-${month}-${day}`;
}
