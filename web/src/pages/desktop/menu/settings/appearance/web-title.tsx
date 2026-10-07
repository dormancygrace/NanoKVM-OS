import { useEffect, useState } from 'react';
import { Input } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { webTitleAtom } from '@/jotai/settings.ts';

export const WebTitle = () => {
  const { t } = useTranslation();
  const [webTitle, setWebTitle] = useAtom(webTitleAtom);

  const [isLoading, setIsLoading] = useState(false);

  useEffect(() => {
    setIsLoading(true);

    api
      .getWebTitle()
      .then((rsp) => {
        if (rsp.data?.title) {
          setWebTitle(rsp.data.title);
        }
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsLoading(false);
      });
  }, [setWebTitle]);

  function submit() {
    if (isLoading) return;
    setIsLoading(true);

    api
      .setWebTitle(webTitle)
      .then((rsp) => {
        if (rsp.code !== 0) showRequestError(rsp);
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <div className="mt-8 flex items-center justify-between space-x-5">
      <div className="flex flex-col">
        <span>{t('settings.appearance.webTitle')}</span>
        <span className="text-fg-muted text-xs">{t('settings.appearance.webTitleDesc')}</span>
      </div>

      <div>
        <Input
          disabled={isLoading}
          style={{ width: 180 }}
          value={webTitle}
          onChange={(e) => setWebTitle(e.target.value)}
          onPressEnter={submit}
          onBlur={submit}
          placeholder="NanoKVM OS"
        />
      </div>
    </div>
  );
};
