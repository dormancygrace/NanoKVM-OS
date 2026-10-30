import { useRef, useState } from 'react';
import { Button, message } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { http, type ApiResponse } from '@/lib/http';
import type { Branding as BrandingState } from '@/jotai/branding';
import { brandingAtom, brandingFavicon, brandingLogo } from '@/jotai/branding';
import { SettingRow } from '@/components/ui/settings.tsx';

type Asset = 'logo' | 'favicon';

// The login logo and the favicon: one row each, a small preview and the
// upload / built-in buttons.
export const Branding = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.appearance.branding.${key}`);
  const [branding, setBranding] = useAtom(brandingAtom);
  const [busy, setBusy] = useState<Asset>();
  const logoInput = useRef<HTMLInputElement>(null);
  const faviconInput = useRef<HTMLInputElement>(null);

  async function update(asset: Asset, action: () => Promise<ApiResponse<BrandingState>>) {
    setBusy(asset);
    try {
      const rsp = await action();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setBranding(rsp.data);
    } catch {
      message.error(tr('failed'));
    } finally {
      setBusy(undefined);
    }
  }

  async function upload(asset: Asset, file?: File) {
    if (!file) return;
    if (file.size > 2 * 1024 * 1024 || !['image/png', 'image/jpeg'].includes(file.type)) {
      message.error(tr('formats'));
      return;
    }
    await update(asset, () =>
      http.post<BrandingState>(`/api/branding/${asset}`, file, {
        headers: { 'Content-Type': file.type }
      })
    );
  }

  const assets = [
    {
      asset: 'logo' as const,
      preview: brandingLogo(branding),
      custom: branding.customLogoAvailable,
      input: logoInput,
      previewClassName: 'h-8 w-20'
    },
    {
      asset: 'favicon' as const,
      preview: brandingFavicon(branding),
      custom: branding.customFaviconAvailable,
      input: faviconInput,
      previewClassName: 'size-8'
    }
  ];

  return (
    <>
      {assets.map(({ asset, preview, custom, input, previewClassName }) => (
        <SettingRow
          key={asset}
          label={tr(`${asset}Title`)}
          description={`${tr(`${asset}Description`)} ${custom ? tr('customActive') : tr('defaultActive')}.`}
        >
          <div className="flex flex-wrap items-center gap-2">
            <div className="bg-surface-raised flex h-10 items-center justify-center rounded-md px-2">
              <img
                src={preview}
                alt={tr(`${asset}Preview`)}
                className={`${previewClassName} object-contain`}
              />
            </div>
            <input
              ref={input}
              type="file"
              accept="image/png,image/jpeg"
              aria-label={tr(`${asset}Upload`)}
              className="hidden"
              onChange={(event) => {
                void upload(asset, event.target.files?.[0]);
                event.target.value = '';
              }}
            />
            <Button
              disabled={busy !== undefined}
              loading={busy === asset}
              onClick={() => input.current?.click()}
            >
              {tr('upload')}
            </Button>
            {custom && (
              <Button
                type="text"
                disabled={busy !== undefined}
                onClick={() =>
                  void update(asset, () => http.delete<BrandingState>(`/api/branding/${asset}`))
                }
              >
                {tr('restoreDefault')}
              </Button>
            )}
          </div>
        </SettingRow>
      ))}
      <p className="text-fg-muted m-0 text-xs">{tr('formats')}</p>
    </>
  );
};
