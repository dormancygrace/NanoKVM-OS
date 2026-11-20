import { useEffect } from 'react';
import { Button } from 'antd';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { ScanTextIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getScreenTextSettings } from '@/api/vm.ts';
import { isHdmiEnabledAtom, screenTextOpenAtom, screenTextSettingsAtom } from '@/jotai/screen.ts';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';

// Reads the text on the screen with the TPU; shown once the models of a
// language are installed, unless text recognition is off (Settings > AI).
export const ScreenText = () => {
  const { t } = useTranslation();
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  const [settings, setSettings] = useAtom(screenTextSettingsAtom);
  const setOpen = useSetAtom(screenTextOpenAtom);

  useEffect(() => {
    if (settings) return;
    let active = true;
    getScreenTextSettings()
      .then((response) => {
        if (active && response.code === 0) setSettings(response.data);
      })
      .catch(() => {
        /* Without the settings the button stays hidden. */
      });
    return () => {
      active = false;
    };
  }, [settings, setSettings]);

  if (!settings?.installed || !settings.enabled) return null;

  const title = captureEnabled ? t('screenText.read') : t('screenText.captureDisabled');
  return (
    <MenuTooltip title={title}>
      <span className="flex shrink-0">
        <Button
          type="text"
          aria-label={title}
          disabled={!captureEnabled}
          className={`flex! h-[30px]! w-[30px]! min-w-[30px]! items-center! justify-center! !p-0 [&_.ant-btn-icon]:flex! [&_.ant-btn-icon]:items-center! [&_svg]:block ${captureEnabled ? 'hover:!text-fg-strong !text-neutral-300' : '!text-neutral-500'}`}
          onClick={() => setOpen(true)}
          icon={<ScanTextIcon size={19} strokeWidth={1.7} />}
        />
      </span>
    </MenuTooltip>
  );
};
