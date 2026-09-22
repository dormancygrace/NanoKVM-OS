import { useEffect, useState } from 'react';
import { Button, ColorPicker, Input, message } from 'antd';
import { useAtom } from 'jotai';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import {
  brandingAtom,
  brandingButtonColor,
  buttonTextColor,
  DEFAULT_BUTTON_COLOR
} from '@/jotai/branding';

const normalizeColor = (value: string) => value.trim().toUpperCase();
const isColor = (value: string) => /^#[0-9A-F]{6}$/.test(value);

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

  async function update(action: () => ReturnType<typeof http.get>) {
    setBusy(true);
    try {
      const rsp = await action();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setBranding(rsp.data);
      setDraft(brandingButtonColor(rsp.data));
    } catch {
      message.error(tr('failed'));
    } finally {
      setBusy(false);
    }
  }

  const previewColor = valid ? normalizedDraft : currentColor;

  return (
    <div className="mt-8 flex flex-col gap-5">
      <div className="flex flex-col gap-1">
        <span>{tr('title')}</span>
        <span className="text-xs text-neutral-500">{tr('description')}</span>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-4 rounded-lg border border-neutral-700/70 bg-neutral-800/30 p-4">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-4">
          <div className="flex h-28 w-full max-w-56 flex-none items-center justify-center rounded-md bg-neutral-900/70 p-3 sm:flex-1 sm:basis-48">
            <Button
              type="primary"
              style={{
                backgroundColor: previewColor,
                borderColor: previewColor,
                color: buttonTextColor(previewColor)
              }}
            >
              {tr('preview')}
            </Button>
          </div>
          <div className="flex w-full min-w-0 flex-none flex-col gap-1 sm:flex-1 sm:basis-40">
            <span className="text-neutral-200">{tr('accentTitle')}</span>
            <span className="text-xs text-neutral-500">{tr('accentDescription')}</span>
            <span className="text-xs text-neutral-400">
              {branding.customButtonColor ? tr('customActive') : tr('defaultActive')}
            </span>
          </div>
        </div>

        <div className="flex w-full max-w-full min-w-0 flex-wrap items-center gap-2 sm:w-auto">
          <ColorPicker
            value={valid ? normalizedDraft : DEFAULT_BUTTON_COLOR}
            disabledAlpha
            disabled={busy}
            onChange={(color) => setDraft(color.toHexString().toUpperCase())}
          />
          <Input
            aria-label={tr('inputLabel')}
            className="w-28 font-mono"
            value={draft}
            maxLength={7}
            status={valid ? undefined : 'error'}
            disabled={busy}
            onChange={(event) => setDraft(event.target.value)}
          />
          <Button
            type="primary"
            loading={busy}
            disabled={!valid || normalizedDraft === currentColor}
            onClick={() =>
              update(() => http.post('/api/branding/button-color', { color: normalizedDraft }))
            }
          >
            {tr('save')}
          </Button>
          {branding.customButtonColor && (
            <Button
              disabled={busy}
              onClick={() => update(() => http.delete('/api/branding/button-color'))}
            >
              {tr('restoreDefault')}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
};
