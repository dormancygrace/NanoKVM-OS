import { message } from 'antd';
import type { TFunction } from 'i18next';

// Matches inputOwnerRequiredCode in server/router/input_owner.go.
const inputOwnerRequiredCode = -4;

// ATX actions are refused for view-only sessions; say so instead of failing silently.
export function reportGpioResult(rsp: { code: number; msg: string }, t: TFunction) {
  if (rsp.code === 0) return;
  message.error(rsp.code === inputOwnerRequiredCode ? t('power.controlRequired') : rsp.msg);
}
