import { useEffect, useState } from 'react';
import { Input } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { webTitleAtom } from '@/jotai/settings.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

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
    <SettingRow
      label={t('settings.appearance.webTitle')}
      description={t('settings.appearance.webTitleDesc')}
      htmlFor="appearance-web-title"
    >
      <Input
        id="appearance-web-title"
        aria-describedby="appearance-web-title-description"
        disabled={isLoading}
        style={{ width: 180 }}
        value={webTitle}
        onChange={(e) => setWebTitle(e.target.value)}
        onPressEnter={submit}
        onBlur={submit}
        placeholder="NanoKVM OS"
      />
    </SettingRow>
  );
};
