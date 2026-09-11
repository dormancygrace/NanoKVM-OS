import { useEffect, useState } from 'react';
import { GithubOutlined } from '@ant-design/icons';
import { ArrowUpRightIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getInfo } from '@/api/vm';
import { formatVersion } from '@/lib/version';

import { Community } from './community';

export const About = () => {
  const { t } = useTranslation();
  const [version, setVersion] = useState('');
  useEffect(() => {
    let active = true;
    getInfo()
      .then((rsp) => {
        if (active && rsp.code === 0) setVersion(rsp.data.application);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);
  return (
    <div className="space-y-8 py-3">
      <div className="space-y-3">
        <a
          href="https://github.com/dormancygrace/NanoKVM-OS"
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-3 text-2xl font-medium !text-neutral-100 hover:!text-blue-400"
        >
          <GithubOutlined className="text-xl" />
          NanoKVM OS
          <ArrowUpRightIcon size={18} className="text-neutral-500" />
        </a>
        <p className="text-sm text-neutral-400">{version ? `v${formatVersion(version)}` : '—'}</p>
        <p className="text-sm leading-relaxed text-neutral-300">
          {t('settings.about.description')}
        </p>
      </div>
      <Community />
    </div>
  );
};
