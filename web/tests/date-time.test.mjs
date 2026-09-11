import { test } from 'node:test';
import assert from 'node:assert/strict';
import { formatDeviceTime, defaultTimePreferences } from '../src/lib/date-time.ts';

test('24-hour default ignores en-US browser preference and uses 00 at midnight', () => {
 assert.equal(formatDeviceTime(Date.parse('2026-01-01T00:00:00Z'), defaultTimePreferences, 'en-US'), '00:00:00');
 assert.equal(formatDeviceTime(Date.parse('2026-01-01T14:30:12Z'), defaultTimePreferences, 'en-US'), '14:30:12');
});
test('explicit 12-hour choice and selected timezone affect handshake timestamps', () => {
 const time = Date.parse('2026-07-01T11:30:00Z');
 assert.equal(formatDeviceTime(time, { timezone: 'Asia/Jerusalem', format: '24' }, 'en-US'), '14:30:00');
 assert.match(formatDeviceTime(time, { timezone: 'Asia/Jerusalem', format: '12' }, 'en-US'), /02:30:00 PM/);
 assert.equal(formatDeviceTime(Date.parse('2026-01-01T11:30:00Z'), { timezone: 'Asia/Jerusalem', format: '24' }, 'en-US'), '13:30:00');
});
