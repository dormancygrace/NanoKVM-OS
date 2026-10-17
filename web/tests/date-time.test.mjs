import assert from 'node:assert/strict';
import { test } from 'node:test';

import { defaultTimePreferences, formatDeviceDay, formatDeviceTime } from '../src/lib/date-time.ts';

test('24-hour default ignores en-US browser preference and uses 00 at midnight', () => {
  assert.equal(
    formatDeviceTime(Date.parse('2026-01-01T00:00:00Z'), defaultTimePreferences, 'en-US'),
    '00:00:00'
  );
  assert.equal(
    formatDeviceTime(Date.parse('2026-01-01T14:30:12Z'), defaultTimePreferences, 'en-US'),
    '14:30:12'
  );
});
test('explicit 12-hour choice and selected timezone affect handshake timestamps', () => {
  const time = Date.parse('2026-07-01T11:30:00Z');
  assert.equal(
    formatDeviceTime(time, { timezone: 'Asia/Jerusalem', format: '24' }, 'en-US'),
    '14:30:00'
  );
  assert.match(
    formatDeviceTime(time, { timezone: 'Asia/Jerusalem', format: '12' }, 'en-US'),
    /02:30:00 PM/
  );
  assert.equal(
    formatDeviceTime(
      Date.parse('2026-01-01T11:30:00Z'),
      { timezone: 'Asia/Jerusalem', format: '24' },
      'en-US'
    ),
    '13:30:00'
  );
});
test('a chosen date order applies in every language, in the device time zone', () => {
  // 22:30 UTC on 9 Oct is 10 Oct in Jerusalem.
  const time = Date.parse('2026-10-09T22:30:05Z');
  const zone = { timezone: 'Asia/Jerusalem', format: '24' };
  for (const locale of ['en-US', 'ru', 'ja']) {
    assert.equal(
      formatDeviceTime(time, { ...zone, dateFormat: 'dmy' }, locale, true),
      '10.10.2026 01:30:05'
    );
    assert.equal(
      formatDeviceTime(time, { ...zone, dateFormat: 'mdy' }, locale, true),
      '10/10/2026 01:30:05'
    );
    assert.equal(formatDeviceDay(time, { ...zone, dateFormat: 'ymd' }, locale), '2026-10-10');
  }
  assert.equal(
    formatDeviceDay(Date.parse('2026-03-05T12:00:00Z'), { ...zone, dateFormat: 'dmy' }, 'en-US'),
    '05.03.2026'
  );
  // Without a choice the language decides, as before.
  assert.equal(formatDeviceDay(time, zone, 'en-US'), 'Oct 10, 2026');
  assert.equal(
    formatDeviceTime(time, { ...zone, dateFormat: 'auto' }, 'en-US', true),
    formatDeviceTime(time, zone, 'en-US', true)
  );
});
