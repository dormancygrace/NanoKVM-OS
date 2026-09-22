import { useEffect, useState } from 'react';
import { GithubOutlined } from '@ant-design/icons';
import { ArrowUpRightIcon, HeartIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getInfo } from '@/api/vm';
import { formatVersion } from '@/lib/version';

import { Community } from './community';
import { Credits } from './credits';

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
      <div className="flex flex-col items-center space-y-3 text-center">
        <a
          href="https://github.com/dormancygrace/NanoKVM-OS"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="NanoKVM OS — GitHub"
          className="group relative inline-flex rounded-lg p-2 transition-colors hover:bg-neutral-800/60"
        >
          <img
            src="/nanokvm-os-connection-full.svg?v=2"
            alt="NanoKVM OS"
            className="h-36 w-60 object-contain"
          />
          <ArrowUpRightIcon
            size={17}
            className="absolute top-3 right-3 text-neutral-600 transition-colors group-hover:text-neutral-300"
          />
        </a>
        <p className="text-sm text-neutral-400">{version ? `v${formatVersion(version)}` : '—'}</p>
        <p className="max-w-xl text-sm leading-relaxed text-neutral-300">
          {t('settings.about.description')}
        </p>
        <div className="flex flex-wrap items-center justify-center gap-3 pt-2">
          <Credits />
          <a
            href="https://github.com/dormancygrace/NanoKVM-OS"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 text-sm !text-neutral-300 hover:!text-white"
          >
            <GithubOutlined />
            GitHub
          </a>
        </div>
      </div>

      <div className="flex gap-4 rounded-lg border border-neutral-700/70 bg-neutral-800/30 p-5">
        <HeartIcon className="mt-0.5 shrink-0 text-[#f43f5e]" size={22} fill="currentColor" />
        <div className="space-y-1">
          <h3 className="font-medium text-neutral-100">{t('settings.about.specialThanksTitle')}</h3>
          <p className="text-sm leading-relaxed text-neutral-400">
            {t('settings.about.specialThanksWife')}
          </p>
        </div>
      </div>

      <Community />
    </div>
  );
};
