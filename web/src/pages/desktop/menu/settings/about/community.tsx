import { BookOpenIcon, BugIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export const Community = () => {
  const { t } = useTranslation();
  const links = [
    {
      label: t('settings.about.documentation'),
      icon: <BookOpenIcon size={17} />,
      href: 'https://github.com/dormancygrace/NanoKVM-OS/tree/main/docs'
    },
    {
      label: t('settings.about.reportIssue'),
      icon: <BugIcon size={17} />,
      href: 'https://github.com/dormancygrace/NanoKVM-OS/issues'
    }
  ];
  return (
    <div className="space-y-8">
      <div className="flex flex-wrap gap-x-6 gap-y-3">
        {links.map((link) => (
          <a
            key={link.href}
            href={link.href}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 text-sm !text-blue-400 hover:!text-blue-300"
          >
            {link.icon}
            {link.label}
          </a>
        ))}
      </div>
      <div className="space-y-2 border-t border-neutral-800 pt-5 text-sm leading-relaxed text-neutral-400">
        <p>{t('settings.about.upstreamCredit')}</p>
        <a
          href="https://github.com/sipeed/NanoKVM"
          target="_blank"
          rel="noopener noreferrer"
          className="inline-block !text-neutral-300 underline decoration-neutral-600 underline-offset-4 hover:!text-white"
        >
          Sipeed NanoKVM
        </a>
      </div>
    </div>
  );
};
