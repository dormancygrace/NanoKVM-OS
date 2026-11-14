import { message } from 'antd';
import type { TFunction } from 'i18next';

import * as api from '@/api/script.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { confirmAction } from '@/components/ui/confirm.ts';

// A .py script needs Python 3, which is not part of the OS: offer to install it.
export async function offerPython(t: TFunction) {
  const confirmed = await confirmAction({
    title: t('script.pythonMissing'),
    content: t('script.pythonMissingDesc'),
    okText: t('script.pythonInstall'),
    cancelText: t('script.cancel')
  });
  if (!confirmed) return;
  const key = 'install-python';
  void message.loading({ key, content: t('script.pythonInstalling'), duration: 0 });
  try {
    const rsp = await api.installPython();
    if (rsp.code !== 0) {
      message.destroy(key);
      showRequestError(rsp);
      return;
    }
    void message.success({ key, content: t('script.pythonInstalled') });
  } catch (err) {
    message.destroy(key);
    showRequestError(err);
  }
}
