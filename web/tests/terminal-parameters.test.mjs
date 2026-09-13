import assert from 'node:assert/strict';
import test from 'node:test';
import { buildSerialQuery } from '../src/pages/terminal/validater.ts';
const defaults = { port: '/dev/ttyS1', baud: null, parity: null, flowControl: null, dataBits: null, stopBits: null };
test('physical UART keeps normalized line settings', () => {
 const q = new URLSearchParams(buildSerialQuery({ ...defaults, baud: ' 9600 ', parity: ' EVEN ' }));
 assert.equal(q.get('baud'), '9600'); assert.equal(q.get('parity'), 'even'); assert.equal(q.get('dataBits'),'8');
});
test('USB serial has no fictional baud settings', () => {
 const q = new URLSearchParams(buildSerialQuery({ ...defaults, port:'/dev/ttyGS0', baud:'9600' }));
 assert.deepEqual([...q], [['port','/dev/ttyGS0']]);
});
test('invalid devices and physical parameters are rejected', () => {
 for (const port of ['/dev/a/b','/dev/a;id','/dev/../root','/dev/null','/etc/passwd']) assert.equal(buildSerialQuery({...defaults,port}),null);
 for (const baud of ['12345','9600;id']) assert.equal(buildSerialQuery({...defaults,baud}),null);
});
