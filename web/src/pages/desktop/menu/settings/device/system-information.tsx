import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { getInfo } from '@/api/vm';
import { formatVersion } from '@/lib/version';

export const SystemInformation = () => {
  const { t } = useTranslation();
  const [info, setInfo] = useState<{ image: string; application: string }>();
  useEffect(() => {
    let active = true;
    getInfo()
      .then((rsp) => {
        if (active && rsp.code === 0) setInfo(rsp.data);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);
  return (
    <details className="text-sm text-neutral-400">
      <summary className="cursor-pointer hover:text-neutral-200">
        {t('settings.about.systemInformation')}
      </summary>
      <dl className="mt-4 space-y-3">
        <div className="flex justify-between gap-4">
          <dt>{t('settings.about.image')}</dt>
          <dd className="text-right">{info?.image || '—'}</dd>
        </div>
        <div className="flex justify-between gap-4">
          <dt>{t('settings.about.application')}</dt>
          <dd className="text-right">
            {info?.application ? formatVersion(info.application) : '—'}
          </dd>
        </div>
      </dl>
    </details>
  );
};
