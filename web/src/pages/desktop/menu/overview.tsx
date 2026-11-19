import { useAtomValue } from 'jotai';
import { LayoutDashboardIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';

import { brandingAtom } from '@/jotai/branding';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';

// Back to the overview, on a device whose start page it is.
export const OverviewLink = () => {
  const { t } = useTranslation();
  const branding = useAtomValue(brandingAtom);
  if (branding.startPage !== 'overview') return null;
  const label = t('overview.title');
  return (
    <MenuTooltip title={label}>
      <Link
        to="/overview"
        aria-label={label}
        data-toolbar-control
        className="hover:text-fg-strong flex h-[30px] w-[30px] items-center justify-center rounded text-neutral-300 hover:bg-neutral-700/80"
      >
        <LayoutDashboardIcon size={18} />
      </Link>
    </MenuTooltip>
  );
};
