import assert from 'node:assert/strict';
import test from 'node:test';

import { operationDone } from '../src/pages/desktop/menu/settings/network/wifi-pending.ts';

const status = (fields) => ({ supported: true, enabled: true, connected: false, ...fields });

test('disconnect is done when the device joined another saved network before the poll', () => {
  const leaving = { disconnect: true, leaving: 'Office' };
  assert.equal(operationDone(leaving, status({ connected: true, ssid: 'Office' })), false);
  assert.equal(operationDone(leaving, status({ connected: true, ssid: 'Cafe' })), true);
  assert.equal(operationDone(leaving, status({ connected: false })), true);
});

test('joining and switching keep their completion rules', () => {
  assert.equal(operationDone({ ssid: 'Cafe' }, status({ connected: true, ssid: 'Office' })), false);
  assert.equal(operationDone({ ssid: 'Cafe' }, status({ connected: true, ssid: 'Cafe' })), true);
  assert.equal(operationDone({ enabled: false }, status({ enabled: false })), true);
  assert.equal(operationDone({ enabled: false }, status({ enabled: true })), false);
});
