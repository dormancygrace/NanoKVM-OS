import assert from 'node:assert/strict';
import test from 'node:test';

import { fetchCommands, scriptArgs } from '../src/lib/serial-console-commands.ts';

const sha = 'a'.repeat(64);
const origin = { https: true, browser: 'https://192.168.1.20' };

test('every fetch command checks the hash before it runs the script', () => {
  const commands = fetchCommands(origin, '10.0.0.1', sha, ' --serial abc');
  for (const command of [commands.network, commands.usb, commands.wget]) {
    const check = command.indexOf(`echo '${sha}  nanokvm-serial-console.sh' | sha256sum -c - && `);
    const run = command.indexOf('sh nanokvm-serial-console.sh --serial abc');
    assert.ok(check > 0, command);
    assert.ok(run > check, command);
  }
  assert.match(commands.network, /^curl -fsS -k https:\/\/192\.168\.1\.20\//);
  assert.match(commands.usb, /^curl -fsS -k https:\/\/10\.0\.0\.1\//);
});

test('the arguments carry the serial number and a changed identity', () => {
  assert.equal(scriptArgs(undefined), '');
  assert.equal(
    scriptArgs({ serialNumber: 'abc', vendorId: '3346', productId: '1009' }),
    ' --serial abc'
  );
  assert.equal(
    scriptArgs({ serialNumber: 'abc', vendorId: '046d', productId: '1009' }),
    ' --serial abc --vid 046d --pid 1009'
  );
  assert.equal(scriptArgs({ serialNumber: '', vendorId: '', productId: '' }), '');
});
