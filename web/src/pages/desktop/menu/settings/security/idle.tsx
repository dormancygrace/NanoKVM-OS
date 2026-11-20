import { useEffect, useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Select } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/auth.ts';
import { IDLE_CHANGED_EVENT } from '@/lib/auth-events.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

// How long a sign-in may show no input before the device ends it. Nothing is
// shown with authentication disabled: there is no sign-in to end.
export const IdleLimit = () => {
  const { t } = useTranslation();
  const { account } = useAuth();
  const [state, setState] = useState<api.IdleState | null>(null);
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    api.getIdle().then((rsp) => {
      if (rsp.code === 0 && rsp.data) setState(rsp.data);
    });
  }, []);

  if (!state?.available) return null;

  function label(seconds: number) {
    if (seconds === 0) return t('settings.security.idle.off');
    return seconds < 3600
      ? t('settings.security.idle.minutes', { n: seconds / 60 })
      : t('settings.security.idle.hours', { n: seconds / 3600 });
  }

  async function change(limit: number) {
    setIsSaving(true);
    try {
      const rsp = await api.setIdle(limit);
      if (rsp.code !== 0 || !rsp.data) {
        showRequestError(rsp, 'settings.security.idle.failed');
        return;
      }
      setState(rsp.data);
      // The page's own watch of the limit learns of it now.
      window.dispatchEvent(new Event(IDLE_CHANGED_EVENT));
    } finally {
      setIsSaving(false);
    }
  }

  return (
    <SettingsSection>
      <SettingRow
        label={t('settings.security.idle.label')}
        description={t('settings.security.idle.description')}
        htmlFor="idle-limit"
      >
        <Select
          id="idle-limit"
          aria-describedby="idle-limit-description"
          className="min-w-32"
          value={state.limit}
          options={state.limits.map((seconds) => ({ value: seconds, label: label(seconds) }))}
          disabled={account.role !== 'admin'}
          loading={isSaving}
          onChange={change}
        />
      </SettingRow>
    </SettingsSection>
  );
};
