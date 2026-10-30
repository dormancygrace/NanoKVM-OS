import { useEffect, useRef, useState } from 'react';
import { Input, message } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { webTitleAtom } from '@/jotai/settings.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

const DEFAULT_TITLE = 'NanoKVM OS';
// The server stores no file for the default title; an empty field means it.
const effective = (title: string) => title.trim() || DEFAULT_TITLE;

export const WebTitle = () => {
  const { t } = useTranslation();
  const [webTitle, setWebTitle] = useAtom(webTitleAtom);
  const [isLoading, setIsLoading] = useState(false);
  // The title the device has, to save only a change.
  const saved = useRef(DEFAULT_TITLE);

  useEffect(() => {
    setIsLoading(true);

    api
      .getWebTitle()
      .then((rsp) => {
        if (rsp.data?.title) {
          saved.current = rsp.data.title;
          setWebTitle(rsp.data.title);
        }
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsLoading(false);
      });
  }, [setWebTitle]);

  function submit() {
    const next = effective(webTitle);
    if (isLoading || next === saved.current) return;
    setIsLoading(true);

    api
      .setWebTitle(next)
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }
        saved.current = next;
        setWebTitle(next);
        void message.success(t('settings.appearance.saved'));
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
        placeholder={DEFAULT_TITLE}
      />
    </SettingRow>
  );
};
