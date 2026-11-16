import { isAxiosError, isCancel } from 'axios';

import { recentAuthRequiredCode } from '@/lib/recheck.ts';

// Matches middleware.PasswordChangeRequiredCode on the server.
export const passwordChangeRequiredCode = -10;

// Failures the shared HTTP client already handles: aborted requests, an
// expired session (the app returns to the login page), the forced password
// change (the app opens the password page) and a declined password
// confirmation (the user cancelled it).
export function isHandledRequestError(error: unknown) {
  if (isCancel(error)) return true;
  if (!isAxiosError(error)) return false;
  if (error.code === 'ERR_CANCELED') return true;
  const status = error.response?.status;
  return (
    status === 401 ||
    (status === 403 &&
      (error.response?.data?.code === passwordChangeRequiredCode ||
        error.response?.data?.code === recentAuthRequiredCode))
  );
}

function serverMessage(error: unknown) {
  const body = isAxiosError(error) ? error.response?.data : error;
  if (!body || typeof body !== 'object' || !('msg' in body)) return '';
  return typeof body.msg === 'string' ? body.msg.trim() : '';
}

// What to tell the user about a failed request: a rejected request or an API
// response with a non-zero code. The localized fallback leads; the server's
// own message follows as detail. Null when nothing should be shown.
export function requestErrorText(error: unknown, fallback: string): string | null {
  if (isHandledRequestError(error)) return null;
  const detail = serverMessage(error);
  return detail && detail !== fallback ? `${fallback}: ${detail}` : fallback;
}
