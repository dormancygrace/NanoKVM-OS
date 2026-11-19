import { useAtomValue, useSetAtom } from 'jotai';
import { MoonIcon, SunIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as storage from '@/lib/localstorage.ts';
import { themeAtom, themeSettingAtom } from '@/jotai/settings.ts';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';

// One click to the other theme. It chooses that theme for this browser, as
// Settings > Appearance > Theme does: a browser that followed its device stops
// following it until System is chosen there again.
export const ThemeToggle = () => {
  const { t } = useTranslation();
  const theme = useAtomValue(themeAtom);
  const setSetting = useSetAtom(themeSettingAtom);
  const next = theme === 'dark' ? 'light' : 'dark';
  const label = t(next === 'light' ? 'settings.appearance.toLight' : 'settings.appearance.toDark');

  return (
    <MenuTooltip title={label}>
      <button
        type="button"
        aria-label={label}
        className="nanokvm-button-base hover:text-fg-strong flex h-[30px] w-[30px] cursor-pointer items-center justify-center rounded text-neutral-300 hover:bg-neutral-700/80"
        onClick={() => {
          setSetting(next);
          storage.setTheme(next);
        }}
      >
        {next === 'light' ? <SunIcon size={18} /> : <MoonIcon size={18} />}
      </button>
    </MenuTooltip>
  );
};
