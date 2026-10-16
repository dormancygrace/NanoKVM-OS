import assert from 'node:assert/strict';
import test from 'node:test';

import { componentLabel, updateSignature } from '../src/lib/update-components.ts';

test('the notification signature ignores order and changes with a version', () => {
  const a = { name: 'nanokvm-web', installed: '3.1.0-r0', available: '3.1.1-r0' };
  const b = { name: 'nanokvm-app', installed: '3.1.0-r0', available: '3.1.1-r0' };
  assert.equal(updateSignature([a, b]), updateSignature([b, a]));
  assert.notEqual(updateSignature([a]), updateSignature([{ ...a, available: '3.1.2-r0' }]));
  assert.equal(updateSignature([]), '');
});

test('known packages get their component name, others keep the package name', () => {
  const t = (key) => `t:${key}`;
  assert.equal(componentLabel(t, 'nanokvm-web'), 't:settings.updates.components.names.web');
  assert.equal(
    componentLabel(t, 'nanokvm-kernel-sg2002'),
    't:settings.updates.components.names.kernel'
  );
  assert.equal(componentLabel(t, 'nanokvm-future'), 'nanokvm-future');
});
