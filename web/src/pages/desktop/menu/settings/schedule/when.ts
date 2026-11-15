import type { TFunction } from 'i18next';

import { formatClock, type TimePreferences } from '@/lib/date-time.ts';

// The schedule forms of the editor and the cron expressions they make.
export type When =
  | { mode: 'minutes'; every: number }
  | { mode: 'hourly'; minute: number }
  | { mode: 'daily'; hour: number; minute: number }
  | { mode: 'weekly'; days: number[]; hour: number; minute: number }
  | { mode: 'monthly'; day: number; hour: number; minute: number }
  | { mode: 'custom'; expression: string };

export function toCron(when: When): string {
  switch (when.mode) {
    case 'minutes':
      return `*/${when.every} * * * *`;
    case 'hourly':
      return `${when.minute} * * * *`;
    case 'daily':
      return `${when.minute} ${when.hour} * * *`;
    case 'weekly':
      // No day is no schedule (the editor refuses it), never every day.
      return `${when.minute} ${when.hour} * * ${[...when.days].sort().join(',')}`;
    case 'monthly':
      return `${when.minute} ${when.hour} ${when.day} * *`;
    case 'custom':
      return when.expression.trim().split(/\s+/).join(' ');
  }
}

const n = Number;

// The editor form that makes this expression, or the expression itself.
export function fromCron(expression: string): When {
  const e = expression.trim().split(/\s+/).join(' ');
  let m = /^\*\/(\d{1,2}) \* \* \* \*$/.exec(e);
  if (m) return { mode: 'minutes', every: n(m[1]) };
  m = /^(\d{1,2}) \* \* \* \*$/.exec(e);
  if (m) return { mode: 'hourly', minute: n(m[1]) };
  m = /^(\d{1,2}) (\d{1,2}) \* \* \*$/.exec(e);
  if (m) return { mode: 'daily', minute: n(m[1]), hour: n(m[2]) };
  m = /^(\d{1,2}) (\d{1,2}) \* \* ([0-6](?:,[0-6])*)$/.exec(e);
  if (m) return { mode: 'weekly', minute: n(m[1]), hour: n(m[2]), days: m[3].split(',').map(n) };
  m = /^(\d{1,2}) (\d{1,2}) (\d{1,2}) \* \*$/.exec(e);
  if (m) return { mode: 'monthly', minute: n(m[1]), hour: n(m[2]), day: n(m[3]) };
  return { mode: 'custom', expression: e };
}

const pad = (v: number) => String(v).padStart(2, '0');

// Weekday names in the interface language: 0 is Sunday, as in cron.
export function weekdayName(day: number, locale: string, width: 'short' | 'long' = 'short') {
  // 2026-11-15 was a Sunday.
  return new Intl.DateTimeFormat(locale, { weekday: width, timeZone: 'UTC' }).format(
    new Date(Date.UTC(2026, 10, 15 + day))
  );
}

// The schedule in words.
export function describe(
  expression: string,
  t: TFunction,
  locale: string,
  preferences: TimePreferences
): string {
  const clock = (hour: number, minute: number) => formatClock(hour, minute, preferences, locale);
  const when = fromCron(expression);
  switch (when.mode) {
    case 'minutes':
      return t('settings.schedule.descMinutes', { n: when.every });
    case 'hourly':
      return t('settings.schedule.descHourly', { m: pad(when.minute) });
    case 'daily':
      return t('settings.schedule.descDaily', { time: clock(when.hour, when.minute) });
    case 'weekly':
      return t('settings.schedule.descWeekly', {
        days: [...when.days]
          .sort()
          .map((d) => weekdayName(d, locale))
          .join(', '),
        time: clock(when.hour, when.minute)
      });
    case 'monthly':
      return t('settings.schedule.descMonthly', {
        day: when.day,
        time: clock(when.hour, when.minute)
      });
    case 'custom':
      return t('settings.schedule.descCustom', { expression: when.expression });
  }
}
