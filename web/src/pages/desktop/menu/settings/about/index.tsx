import { useEffect, useState } from 'react';
import { ArrowUpRightIcon, HeartIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getInfo } from '@/api/vm';
import { formatVersion } from '@/lib/version';
import { GithubIcon } from '@/components/icons/github';
import { Panel } from '@/components/ui/settings.tsx';

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
    <div className="space-y-6">
      <div className="flex flex-col items-center space-y-3 text-center">
        <a
          href="https://github.com/dormancygrace/NanoKVM-OS"
          target="_blank"
          rel="noopener noreferrer"
          aria-label="NanoKVM OS — GitHub"
          className="group hover:bg-surface-raised relative inline-flex rounded-lg p-2 transition-colors"
        >
          <img
            src="/nanokvm-os-connection-full.svg?v=2"
            alt="NanoKVM OS"
            className="h-36 w-60 object-contain"
          />
          <ArrowUpRightIcon
            size={16}
            className="text-fg-muted group-hover:text-fg absolute top-3 right-3 transition-colors"
          />
        </a>
        <p className="text-fg-muted text-sm">{version ? `v${formatVersion(version)}` : '—'}</p>
        <p className="text-fg max-w-xl text-sm leading-relaxed">
          {t('settings.about.description')}
        </p>
        <div className="flex flex-wrap items-center justify-center gap-3 pt-2">
          <Credits />
          <a
            href="https://github.com/dormancygrace/NanoKVM-OS"
            target="_blank"
            rel="noopener noreferrer"
            className="!text-fg inline-flex items-center gap-2 text-sm hover:!text-white"
          >
            <GithubIcon size={16} />
            GitHub
          </a>
        </div>
      </div>

      <Panel className="flex gap-4">
        <HeartIcon className="mt-0.5 shrink-0 text-[#f43f5e]" size={22} fill="currentColor" />
        <div className="space-y-1">
          <h3 className="text-fg font-medium">{t('settings.about.specialThanksTitle')}</h3>
          <p className="text-fg-muted text-sm leading-relaxed">
            {t('settings.about.specialThanksWife')}
          </p>
        </div>
      </Panel>

      <Community />
    </div>
  );
};
