import assert from 'node:assert/strict';
import test from 'node:test';

import { formValues, localized, submitValues } from '../src/lib/addon-form.ts';

test('a text in the UI language, its base language, then English', () => {
  const text = { en: 'Power', ru: 'Питание', zh: '电源', zh_tw: '電源' };
  assert.equal(localized(text, 'ru'), 'Питание');
  assert.equal(localized(text, 'zh_tw'), '電源');
  assert.equal(localized(text, 'zh-TW'), '電源');
  assert.equal(localized({ en: 'Power', zh: '电源' }, 'zh_tw'), '电源');
  assert.equal(localized(text, 'de'), 'Power');
  assert.equal(localized(undefined, 'ru'), '');
});

const fields = [
  { key: 'enabled', type: 'switch' },
  { key: 'name', type: 'text' },
  { key: 'secret', type: 'password' },
  { key: 'port', type: 'number' },
  { key: 'state', type: 'status' }
];

test('a form sends the edited fields only', () => {
  assert.deepEqual(
    submitValues(fields, { enabled: undefined, name: 'kvm', secret: '', port: 623, state: 'up' }),
    { enabled: false, name: 'kvm', port: 623 }
  );
  assert.deepEqual(submitValues(fields, { enabled: true, secret: 'new password' }), {
    enabled: true,
    secret: 'new password'
  });
});

test('a form starts from the add-on data, passwords empty', () => {
  assert.deepEqual(
    formValues(fields, { enabled: true, name: 'kvm', secret: 'hash', port: 623, state: 'up' }),
    { enabled: true, name: 'kvm', secret: '', port: 623 }
  );
});
