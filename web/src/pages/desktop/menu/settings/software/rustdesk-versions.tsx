import { useTranslation } from 'react-i18next';

import type { RustDeskStatus } from '@/api/rustdesk';

export const RustDeskVersions = ({ status }: { status: RustDeskStatus }) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.rustdesk' });
  return (
    <div className="flex flex-col gap-1 text-sm text-neutral-400">
      <span>
        {t('addonVersion')}: {status.version || t('unknownVersion')}
      </span>
      <span>
        {t('rustdeskVersion')}: {status.rustdesk_version || t('unknownVersion')}
      </span>
    </div>
  );
};
