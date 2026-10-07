import { BookOpenIcon, BugIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export const Community = () => {
  const { t } = useTranslation();
  const links = [
    {
      label: t('settings.about.documentation'),
      icon: <BookOpenIcon size={16} />,
      href: 'https://github.com/dormancygrace/NanoKVM-OS/tree/main/docs'
    },
    {
      label: t('settings.about.reportIssue'),
      icon: <BugIcon size={16} />,
      href: 'https://github.com/dormancygrace/NanoKVM-OS/issues'
    }
  ];
  return (
    <div className="space-y-6">
      <div className="flex flex-wrap gap-x-6 gap-y-3">
        {links.map((link) => (
          <a
            key={link.href}
            href={link.href}
            target="_blank"
            rel="noopener noreferrer"
            className="!text-info hover:!text-info/80 inline-flex items-center gap-2 text-sm"
          >
            {link.icon}
            {link.label}
          </a>
        ))}
      </div>
      <div className="border-line text-fg-muted space-y-2 border-t pt-5 text-sm leading-relaxed">
        <p>{t('settings.about.upstreamCredit')}</p>
        <a
          href="https://github.com/sipeed/NanoKVM"
          target="_blank"
          rel="noopener noreferrer"
          className="!text-fg decoration-line inline-block underline underline-offset-4 hover:!text-white"
        >
          Sipeed NanoKVM
        </a>
      </div>
    </div>
  );
};
