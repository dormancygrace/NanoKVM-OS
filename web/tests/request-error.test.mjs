import assert from 'node:assert/strict';
import test from 'node:test';
import { AxiosError, CanceledError } from 'axios';

import { isHandledRequestError, requestErrorText } from '../src/lib/request-error.ts';

const httpError = (status, data = {}) =>
  new AxiosError('Request failed', 'ERR_BAD_REQUEST', undefined, undefined, {
    status,
    data,
    statusText: '',
    headers: {},
    config: {}
  });

test('aborted requests, expired sessions and the password redirect stay silent', () => {
  assert.equal(isHandledRequestError(new CanceledError()), true);
  assert.equal(isHandledRequestError(httpError(401)), true);
  assert.equal(isHandledRequestError(httpError(403, { code: -10 })), true);
  assert.equal(requestErrorText(httpError(401), 'Failed'), null);
});

test('unexpected failures are shown with the server detail when present', () => {
  assert.equal(isHandledRequestError(httpError(403, { code: -1 })), false);
  assert.equal(requestErrorText(httpError(500), 'Failed'), 'Failed');
  assert.equal(
    requestErrorText(httpError(400, { msg: 'invalid hostname' }), 'Failed'),
    'Failed: invalid hostname'
  );
  assert.equal(requestErrorText({ code: -2, msg: 'busy' }, 'Failed'), 'Failed: busy');
  assert.equal(requestErrorText({ code: -2, msg: '' }, 'Failed'), 'Failed');
  assert.equal(requestErrorText(new Error('Network Error'), 'Failed'), 'Failed');
});
