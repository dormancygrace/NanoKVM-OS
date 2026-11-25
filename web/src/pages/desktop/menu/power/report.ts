import { message } from 'antd';
import type { TFunction } from 'i18next';

// Matches input_owner_code in server-cpp/src/input/keyboard_routes.cpp.
const inputOwnerRequiredCode = -4;

// ATX actions are refused for view-only sessions; say so instead of failing silently.
export function reportGpioResult(rsp: { code: number; msg: string }, t: TFunction) {
  if (rsp.code === 0) return;
  message.error(rsp.code === inputOwnerRequiredCode ? t('power.controlRequired') : rsp.msg);
}
