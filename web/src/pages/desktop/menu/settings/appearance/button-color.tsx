import { useEffect, useState } from 'react';
import { Button, ColorPicker, Input, message } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { http, type ApiResponse } from '@/lib/http';
import type { Branding } from '@/jotai/branding';
import { brandingAtom, brandingButtonColor } from '@/jotai/branding';
import { SettingRow } from '@/components/ui/settings.tsx';

const normalizeColor = (value: string) => value.trim().toUpperCase();
const isColor = (value: string) => /^#[0-9A-F]{6}$/.test(value);

// The colour applies when the palette closes or a complete code is entered;
// the buttons of the page itself are the preview.
export const ButtonColor = () => {
  const { t } = useTranslation();
  const tr = (key: string) => t(`settings.appearance.buttonColor.${key}`);
  const [branding, setBranding] = useAtom(brandingAtom);
  const currentColor = brandingButtonColor(branding);
  const [draft, setDraft] = useState(currentColor);
  const [busy, setBusy] = useState(false);
  const normalizedDraft = normalizeColor(draft);
  const valid = isColor(normalizedDraft);

  useEffect(() => setDraft(currentColor), [currentColor]);

  async function update(action: () => Promise<ApiResponse<Branding>>) {
    setBusy(true);
    try {
      const rsp = await action();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setBranding(rsp.data);
      setDraft(brandingButtonColor(rsp.data));
    } catch {
      setDraft(currentColor);
      message.error(tr('failed'));
    } finally {
      setBusy(false);
    }
  }

  function apply(color: string) {
    const next = normalizeColor(color);
    if (busy || !isColor(next) || next === currentColor) return;
    void update(() => http.post<Branding>('/api/branding/button-color', { color: next }));
  }

  return (
    <SettingRow
      label={tr('title')}
      description={branding.customButtonColor ? tr('description') : tr('defaultActive')}
      htmlFor="appearance-button-color"
    >
      <div className="flex flex-wrap items-center gap-2">
        <ColorPicker
          value={valid ? normalizedDraft : currentColor}
          disabledAlpha
          disabled={busy}
          onChange={(color) => setDraft(color.toHexString().toUpperCase())}
          onOpenChange={(open) => {
            if (!open) apply(draft);
          }}
        />
        <Input
          id="appearance-button-color"
          aria-describedby="appearance-button-color-description"
          aria-label={tr('inputLabel')}
          className="w-28 font-mono"
          value={draft}
          maxLength={7}
          status={valid ? undefined : 'error'}
          disabled={busy}
          onChange={(event) => setDraft(event.target.value)}
          onBlur={() => (valid ? apply(draft) : setDraft(currentColor))}
          onPressEnter={() => apply(draft)}
        />
        {branding.customButtonColor && (
          <Button
            type="text"
            disabled={busy}
            onClick={() => void update(() => http.delete<Branding>('/api/branding/button-color'))}
          >
            {tr('restoreDefault')}
          </Button>
        )}
      </div>
    </SettingRow>
  );
};
