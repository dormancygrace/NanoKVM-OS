import { useState } from 'react';
import { Button, Popconfirm, type TooltipProps } from 'antd';
import { LogOutIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import * as api from '@/api/auth.ts';
import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';

export const Logout = ({
  tooltipPlacement = 'bottom'
}: {
  tooltipPlacement?: TooltipProps['placement'];
}) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [loading, setLoading] = useState(false);

  async function logout() {
    setLoading(true);
    try {
      const rsp = await api.logout();
      if (rsp.code !== 0) return;
      notifyAuthExpired();
      navigate('/auth/login');
    } finally {
      setLoading(false);
    }
  }

  const label = t('settings.account.logoutBtn');

  return (
    <Popconfirm
      placement={tooltipPlacement}
      title={t('settings.account.logoutDesc')}
      okText={t('settings.account.okBtn')}
      cancelText={t('settings.account.cancelBtn')}
      okButtonProps={{ type: 'default' }}
      cancelButtonProps={{ type: 'primary' }}
      onConfirm={logout}
    >
      <MenuTooltip title={label} placement={tooltipPlacement}>
        <Button
          type="text"
          loading={loading}
          aria-label={label}
          className="flex! size-[30px]! min-w-[30px]! items-center! justify-center! !p-0 text-neutral-300 hover:!bg-neutral-700/80 hover:!text-white"
          icon={<LogOutIcon size={18} aria-hidden="true" />}
        />
      </MenuTooltip>
    </Popconfirm>
  );
};
