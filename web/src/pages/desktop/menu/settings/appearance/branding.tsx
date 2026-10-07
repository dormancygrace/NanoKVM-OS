import { useRef, useState } from 'react';
import { Button, message } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { brandingAtom, brandingFavicon, brandingLogo } from '@/jotai/branding';
import { Panel, SettingRow, StatusBadge } from '@/components/ui/settings.tsx';

type Asset = 'logo' | 'favicon';

export const Branding = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.appearance.branding.${key}`);
  const [branding, setBranding] = useAtom(brandingAtom);
  const [busy, setBusy] = useState<Asset>();
  const logoInput = useRef<HTMLInputElement>(null);
  const faviconInput = useRef<HTMLInputElement>(null);

  async function update(asset: Asset, action: () => ReturnType<typeof http.get>) {
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
      http.post(`/api/branding/${asset}`, file, { headers: { 'Content-Type': file.type } })
    );
  }

  const assets = [
    {
      asset: 'logo' as const,
      preview: brandingLogo(branding),
      custom: branding.customLogoAvailable,
      input: logoInput,
      previewClassName: 'h-24 w-52'
    },
    {
      asset: 'favicon' as const,
      preview: brandingFavicon(branding),
      custom: branding.customFaviconAvailable,
      input: faviconInput,
      previewClassName: 'h-16 w-16'
    }
  ];

  return (
    <SettingRow label={tr('title')} description={tr('description')} stacked>
      <div className="space-y-2">
        {assets.map(({ asset, preview, custom, input, previewClassName }) => (
          <Panel key={asset} className="flex flex-wrap items-center justify-between gap-4">
            <div className="flex min-w-0 flex-1 flex-wrap items-center gap-4">
              <div className="bg-surface-raised flex h-28 w-full max-w-56 flex-none items-center justify-center rounded-md p-3 sm:flex-1 sm:basis-48">
                <img
                  src={preview}
                  alt={tr(`${asset}Preview`)}
                  className={`${previewClassName} max-w-full object-contain`}
                />
              </div>
              <div className="flex w-full min-w-0 flex-none flex-col items-start gap-1 sm:flex-1 sm:basis-40">
                <span className="text-fg">{tr(`${asset}Title`)}</span>
                <span className="text-fg-muted text-xs">{tr(`${asset}Description`)}</span>
                <StatusBadge tone={custom ? 'info' : 'neutral'}>
                  {custom ? tr('customActive') : tr('defaultActive')}
                </StatusBadge>
              </div>
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
            <div className="flex w-full max-w-full min-w-0 flex-wrap gap-2 sm:w-auto">
              <Button
                className="h-auto w-full max-w-full py-1 whitespace-normal sm:w-auto [&>span]:break-words [&>span]:whitespace-normal"
                disabled={busy !== undefined}
                loading={busy === asset}
                onClick={() => input.current?.click()}
              >
                {tr(`${asset}Upload`)}
              </Button>
              {custom && (
                <Button
                  className="h-auto w-full max-w-full py-1 whitespace-normal sm:w-auto [&>span]:break-words [&>span]:whitespace-normal"
                  disabled={busy !== undefined}
                  onClick={() => update(asset, () => http.delete(`/api/branding/${asset}`))}
                >
                  {tr('restoreDefault')}
                </Button>
              )}
            </div>
          </Panel>
        ))}

        <div className="text-fg-muted text-xs">{tr('formats')}</div>
      </div>
    </SettingRow>
  );
};
