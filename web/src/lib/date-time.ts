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

// The date alone, for a preview of the date format.
export function formatDeviceDay(value: number, preferences: TimePreferences, locale?: string) {
  const dateFormat = preferences.dateFormat ?? 'auto';
  if (dateFormat === 'auto') {
    return new Intl.DateTimeFormat(locale, {
      timeZone: preferences.timezone,
      year: 'numeric',
      month: 'short',
      day: 'numeric'
    }).format(value);
  }
  return formatDeviceDate(value, preferences.timezone, dateFormat);
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
