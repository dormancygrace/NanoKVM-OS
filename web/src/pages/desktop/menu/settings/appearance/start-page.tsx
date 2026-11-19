import { useState } from 'react';
import { message, Segmented } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import type { Branding } from '@/jotai/branding';
import { brandingAtom } from '@/jotai/branding';
import { SettingRow } from '@/components/ui/settings.tsx';

type Page = NonNullable<Branding['startPage']>;

// What opens after the sign-in, for everyone who opens this device.
export const StartPage = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.appearance.startPage.${key}`);
  const [branding, setBranding] = useAtom(brandingAtom);
  const [busy, setBusy] = useState(false);

  async function change(page: Page) {
    if (busy || page === branding.startPage) return;
    setBusy(true);
    try {
      const rsp = await http.post<Branding>('/api/branding/start-page', { page });
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setBranding(rsp.data);
    } catch {
      message.error(tr('failed'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <SettingRow label={tr('title')} description={tr('description')}>
      <Segmented<Page>
        disabled={busy}
        value={branding.startPage ?? 'screen'}
        options={[
          { value: 'overview', label: tr('overview') },
          { value: 'screen', label: tr('screen') }
        ]}
        onChange={change}
      />
    </SettingRow>
  );
};
