import { useState } from 'react';
import { message, Segmented } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { brandingAtom } from '@/jotai/branding';

type BannerStyle = 'default' | 'rainbow';

export const BannerStyleSetting = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.appearance.bannerStyle.${key}`);
  const [branding, setBranding] = useAtom(brandingAtom);
  const [busy, setBusy] = useState(false);
  const current: BannerStyle = branding.bannerStyle === 'rainbow' ? 'rainbow' : 'default';

  const options = [
    { value: 'default' as const, label: tr('default') },
    { value: 'rainbow' as const, label: tr('rainbow') }
  ];

  async function handleChange(style: BannerStyle) {
    if (style === current || busy) return;
    setBusy(true);
    try {
      const rsp = await http.post('/api/branding/banner-style', { style });
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setBranding(rsp.data);
    } catch {
      message.error(tr('failed'));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="mt-8 flex w-full flex-wrap items-center justify-between gap-4">
      <div className="flex min-w-0 flex-col gap-1">
        <span>{tr('title')}</span>
        <span className="text-xs text-neutral-500">{tr('description')}</span>
      </div>
      <Segmented<BannerStyle>
        value={current}
        options={options}
        disabled={busy}
        onChange={handleChange}
      />
    </div>
  );
};
