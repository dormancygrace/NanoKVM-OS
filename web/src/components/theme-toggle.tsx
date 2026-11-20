import clsx from 'clsx';
import { useAtomValue, useSetAtom } from 'jotai';
import { MoonIcon, SunIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as storage from '@/lib/localstorage.ts';
import { themeAtom, themeSettingAtom } from '@/jotai/settings.ts';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';

// One click to the other theme. It chooses that theme for this browser, as
// Settings > Appearance > Theme does: a browser that followed its device stops
// following it until System is chosen there again.
// bar: a plain button of the console's bar, without the colours of its own.
export const ThemeToggle = ({ bar = false }: { bar?: boolean }) => {
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
        // The colours of where it leads: night violet with a pale moon on a
        // light page, a warm sun on a dark one. Fixed colours: the button looks
        // the same whatever the theme does to the palette.
        data-toolbar-control={bar || undefined}
        className={clsx(
          bar
            ? 'nanokvm-button-base hover:text-fg-strong flex h-[30px] w-[30px] cursor-pointer items-center justify-center rounded text-neutral-300 hover:bg-neutral-700/80'
            : 'nanokvm-button-base flex size-9 cursor-pointer items-center justify-center rounded-full shadow-lg shadow-black/25 transition-colors',
          bar
            ? undefined
            : next === 'dark'
              ? 'bg-[#3b2a7a] text-[#ede9fe] hover:bg-[#4c3799]'
              : 'bg-[#2a2a2a] text-[#fcd34d] outline outline-1 outline-[#525252] hover:bg-[#383838]'
        )}
        onClick={() => {
          setSetting(next);
          storage.setTheme(next);
        }}
      >
        {bar ? (
          next === 'light' ? (
            <SunIcon size={18} />
          ) : (
            <MoonIcon size={18} />
          )
        ) : next === 'light' ? (
          // a filled disc with its rays: the outline alone looked thin
          <SunIcon size={20} strokeWidth={2.4} fill="currentColor" />
        ) : (
          <MoonIcon size={18} />
        )}
      </button>
    </MenuTooltip>
  );
};
