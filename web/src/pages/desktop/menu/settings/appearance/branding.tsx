import { useRef, useState } from 'react';
import { Button, message } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { brandingAtom, brandingLogo, type Branding as BrandingState } from '@/jotai/branding';

export const Branding = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.appearance.branding.${key}`);
  const [branding, setBranding] = useAtom(brandingAtom);
  const [busy, setBusy] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  async function update(action: () => ReturnType<typeof http.get>) {
    setBusy(true);
    try {
      const rsp = await action();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setBranding(rsp.data);
    } catch {
      message.error(tr('failed'));
    } finally {
      setBusy(false);
    }
  }
  async function upload(file?: File) {
    if (!file) return;
    if (file.size > 2 * 1024 * 1024 || !['image/png', 'image/jpeg'].includes(file.type)) {
      message.error(tr('formats'));
      return;
    }
    await update(() =>
      http.post('/api/branding/logo', file, { headers: { 'Content-Type': file.type } })
    );
  }
  const choices: { style: BrandingState['style']; label: string }[] = [
    { style: 'connection', label: tr('connection') },
    { style: 'screen', label: tr('screen') },
    ...(branding.customAvailable ? [{ style: 'custom' as const, label: tr('custom') }] : [])
  ];
  return (
    <div className="mt-8 flex flex-col gap-3">
      <span>{tr('title')}</span>
      <span className="text-xs text-neutral-500">{tr('description')}</span>
      <div className="flex flex-wrap gap-3">
        {choices.map(({ style, label }) => (
          <button
            key={style}
            type="button"
            aria-pressed={branding.style === style}
            disabled={busy}
            onClick={() => update(() => http.post('/api/branding', { style }))}
            className={`flex min-w-32 flex-col items-center gap-2 rounded-lg border p-4 text-sm text-neutral-200 disabled:opacity-50 ${branding.style === style ? 'border-blue-500 bg-blue-500/10' : 'border-neutral-700 bg-neutral-900 hover:border-neutral-500'}`}
          >
            <img
              src={brandingLogo({ ...branding, style })}
              alt=""
              className="h-12 w-12 object-contain"
            />
            {label}
          </button>
        ))}
      </div>
      <input
        ref={input}
        type="file"
        accept="image/png,image/jpeg"
        aria-label={tr('upload')}
        className="hidden"
        onChange={(event) => {
          void upload(event.target.files?.[0]);
          event.target.value = '';
        }}
      />
      <div className="flex flex-wrap gap-2">
        <Button disabled={busy} onClick={() => input.current?.click()}>
          {tr('upload')}
        </Button>
        {branding.customAvailable && (
          <Button disabled={busy} onClick={() => update(() => http.delete('/api/branding/logo'))}>
            {tr('remove')}
          </Button>
        )}
      </div>
      <span className="text-xs text-neutral-500">{tr('formats')}</span>
    </div>
  );
};
