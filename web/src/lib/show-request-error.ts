import { message } from 'antd';
import i18n from 'i18next';

import { requestErrorText } from '@/lib/request-error.ts';

// Show a failed request (a rejection or an API response with a non-zero code)
// with the antd message API, unless the shared client already handled it.
// Takes a translation key so loaders need no `t` dependency.
export function showRequestError(error: unknown, key = 'error.requestFailed') {
  const text = requestErrorText(error, i18n.t(key));
  if (text) void message.error(text);
}
