import assert from 'node:assert/strict';
import test from 'node:test';

import { groupWifiNetworks } from './wifi-networks.ts';

test('merge bands, retain strongest connection candidate, separate security', () => {
  const result = groupWifiNetworks([
    { ssid: 'Shared', bssid: 'a', band: '2.4', security: 'wpa2', signal: -60 },
    { ssid: 'Shared', bssid: 'b', band: '5', security: 'wpa2', signal: -45 },
    { ssid: 'Shared', bssid: 'c', band: '5', security: 'wpa2', signal: -70 },
    { ssid: 'Shared', bssid: 'd', band: '2.4', security: 'open', signal: -40 }
  ]);
  assert.equal(result.length, 2);
  assert.equal(result[0].security, 'open');
  assert.deepEqual(result[1].bands, ['2.4', '5']);
  assert.equal(result[1].band, '5');
  assert.equal(result[1].bssid, 'b');
});

test('combine WPA3 with transition mode without losing per-band authentication', () => {
  const result = groupWifiNetworks([
    { ssid: 'Home', bssid: 'a', band: '2.4', security: 'wpa2-wpa3', signal: -55 },
    { ssid: 'Home', bssid: 'b', band: '5', security: 'wpa3', signal: -65 },
    { ssid: 'Home', bssid: 'c', band: '2.4', security: 'unsupported', signal: -30 }
  ]);
  assert.equal(result.length, 2);
  const home = result.find((item) => item.security !== 'unsupported')!;
  assert.deepEqual(home.bands, ['2.4', '5']);
  assert.equal(home.candidates.find((item) => item.band === '5')?.security, 'wpa3');
});
