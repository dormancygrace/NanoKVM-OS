import assert from 'node:assert/strict';
import test from 'node:test';

import {
  agentInfo,
  groupSensors,
  isShown,
  newerVersion,
  sensorKey,
  sensorValue,
  viewOverrides
} from '../src/lib/host-sensors.ts';

const s = (hw, type, name, kind, value) => ({ hw, type, name, kind, value });

test('parts are listed processor and board first, readings by kind', () => {
  const groups = groupSensors([
    s('Disk', 'storage', 'Temperature', 'temperature', 40),
    s('Board', 'motherboard', 'Fan #1', 'fan', 900),
    s('Odd', 'other', 'X', 'factor', 1),
    s('CPU', 'cpu', 'Package', 'power', 65),
    s('CPU', 'cpu', 'Core', 'temperature', 51.26),
    s('CPU', 'cpu', 'Core 2', 'temperature', 50)
  ]);
  assert.deepEqual(
    groups.map((group) => group.hardware),
    ['CPU', 'Board', 'Disk', 'Odd']
  );
  assert.deepEqual(
    groups[0].sensors.map((sensor) => sensor.name),
    ['Core', 'Core 2', 'Package']
  );
});

test('a value is printed with the unit of its kind', () => {
  assert.equal(sensorValue(s('', '', '', 'temperature', 51.26)), '51.3 °C');
  assert.equal(sensorValue(s('', '', '', 'fan', 912.4)), '912 RPM');
  assert.equal(sensorValue(s('', '', '', 'voltage', 12.0961)), '12.096 V');
  assert.equal(sensorValue(s('', '', '', 'factor', 1)), '1.00');
});

test('the stored choice is the exceptions to the default, kept for absent readings', () => {
  const temp = s('CPU', 'cpu', 'Core', 'temperature', 50);
  const load = s('CPU', 'cpu', 'Total', 'load', 3);
  const gone = 'storage\tOld disk\ttemperature\tTemperature';
  const overrides = viewOverrides(
    [temp, load],
    { [sensorKey(temp)]: false, [sensorKey(load)]: false },
    { [gone]: false, [sensorKey(load)]: true }
  );
  // A hidden temperature is an exception; a hidden load is the default again.
  assert.deepEqual(overrides, { [gone]: false, [sensorKey(temp)]: false });
  assert.equal(isShown(temp, overrides), false);
  assert.equal(isShown(load, overrides), false);
  assert.equal(isShown(s('GPU', 'gpu', 'Core', 'temperature', 40), overrides), true);
});

test('the agent of a report and whether the add-on brings a newer one', () => {
  assert.deepEqual(agentInfo('nanokvm-sensors/1.2.3 windows'), {
    version: '1.2.3',
    system: 'windows'
  });
  assert.equal(agentInfo('fake/0'), undefined);
  assert.equal(newerVersion('1.10.0', '1.9.5'), true);
  assert.equal(newerVersion('1.0.0', '1.0.0'), false);
  assert.equal(newerVersion('1.0', '1.0.1'), false);
});
