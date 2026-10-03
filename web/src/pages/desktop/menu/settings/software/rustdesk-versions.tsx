import { useTranslation } from 'react-i18next';

import type { RustDeskStatus } from '@/api/rustdesk';

import { rustDeskLabels } from './rustdesk-labels';

export const RustDeskVersions = ({ status }: { status: RustDeskStatus }) => {
  const { i18n } = useTranslation();
  const l = rustDeskLabels[(i18n.resolvedLanguage || i18n.language).startsWith('ru') ? 'ru' : 'en'];
  return (
    <div className="flex flex-col gap-1 text-sm text-neutral-400">
      <span>
        {l.addonVersion}: {status.version || l.unknownVersion}
      </span>
      <span>
        {l.rustdeskVersion}: {status.rustdesk_version || l.unknownVersion}
      </span>
    </div>
  );
};
