import assert from 'node:assert/strict';
import test from 'node:test';
import { buildPicocomCommand, validatePicocomParameters } from '../src/pages/terminal/validater.ts';

const defaults = { port: '/dev/ttyGS0', baud: '115200', parity: null, flowControl: null, dataBits: null, stopBits: null };
test('partial serial URL supplies real 8N1 defaults instead of null arguments', () => {
  const command = buildPicocomCommand(defaults);
  assert.match(command, /--parity none --flow none --databits 8 --stopbits 1 /);
  assert.ok(command.endsWith('\r'));
  assert.ok(!command.includes('null'));
});
test('validated normalized values are the values sent to picocom', () => {
  const command = buildPicocomCommand({ ...defaults, baud: ' 9600 ', parity: ' EVEN ', flowControl: ' HARD ' });
  assert.match(command, /--baud 9600 --parity even --flow hard/);
});
test('invalid baud and unexpected device path characters are rejected', () => {
  assert.equal(buildPicocomCommand({ ...defaults, baud: '12345' }), null);
  for (const port of ['/dev/a/b','/dev/a@b','/dev/a[b]','/dev/a;id','/dev/../root','/dev/a$(id)']) {
    assert.equal(validatePicocomParameters({ ...defaults, port }), false, port);
    assert.equal(buildPicocomCommand({ ...defaults, port }), null, port);
  }
  assert.ok(buildPicocomCommand({ ...defaults, port: '/dev/ttyUSB0' }));
});

test('parity choices unsupported by the selected picocom are rejected', () => {
  for (const parity of ['m','s','mark','space']) {
    assert.equal(buildPicocomCommand({ ...defaults, parity }), null);
  }
});
