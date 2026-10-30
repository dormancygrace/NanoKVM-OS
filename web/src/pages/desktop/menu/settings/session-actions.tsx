import { useState } from 'react';
import clsx from 'clsx';
import { LogOutIcon, RotateCwIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router-dom';

import * as authApi from '@/api/auth.ts';
import * as vmApi from '@/api/vm.ts';
import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { confirmAction } from '@/components/ui/confirm.ts';

import styles from './sidebar.module.css';

// Sign out and restart, at the foot of the settings navigation above About:
// actions, not settings, so they are not on a page of their own.
export const SessionActions = ({
  isAdmin,
  disabled,
  mobile
}: {
  isAdmin: boolean;
  disabled: boolean;
  mobile: boolean;
}) => {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [busy, setBusy] = useState<'logout' | 'reboot'>();

  async function logout() {
    if (busy) return;
    if (
      !(await confirmAction({
        title: t('settings.account.logoutDesc'),
        okText: t('settings.account.logoutBtn'),
        cancelText: t('settings.account.cancelBtn'),
        safeDefault: true
      }))
    )
      return;
    setBusy('logout');
    try {
      const rsp = await authApi.logout();
      if (rsp.code !== 0) {
        showRequestError(rsp);
        return;
      }
      notifyAuthExpired();
      navigate('/auth/login');
    } catch (err) {
      showRequestError(err);
    } finally {
      setBusy(undefined);
    }
  }

  async function reboot() {
    if (busy) return;
    if (
      !(await confirmAction({
        title: t('settings.device.rebootDesc'),
        okText: t('settings.device.reboot'),
        cancelText: t('settings.device.cancelBtn'),
        danger: true,
        safeDefault: true
      }))
    )
      return;
    setBusy('reboot');
    // The page comes back by itself once the device is up again.
    const reload = setTimeout(() => window.location.reload(), 30000);
    try {
      const rsp = await vmApi.reboot();
      if (rsp.code !== 0) {
        showRequestError(rsp);
        clearTimeout(reload);
        setBusy(undefined);
      }
    } catch (err) {
      showRequestError(err);
      clearTimeout(reload);
      setBusy(undefined);
    }
  }

  const item = (label: string, icon: React.ReactNode, onClick: () => void, danger = false) => (
    <button
      type="button"
      disabled={disabled || !!busy}
      aria-label={label}
      className={clsx(
        styles.item,
        styles.action,
        danger && styles.danger,
        'flex w-full items-center gap-2 rounded-lg p-2 text-left select-none sm:px-3',
        mobile && 'min-h-12'
      )}
      onClick={onClick}
    >
      <div className="flex h-[18px] w-[18px] shrink-0 items-center justify-center">{icon}</div>
      <span className={mobile ? 'truncate text-sm' : 'hidden truncate text-sm sm:block'}>
        {label}
      </span>
    </button>
  );

  return (
    // A thin line sets the actions apart from the pages above.
    <div className="border-line space-y-0.5 border-t pt-2">
      {item(t('settings.account.logoutBtn'), <LogOutIcon size={16} />, () => void logout())}
      {isAdmin &&
        item(t('settings.device.reboot'), <RotateCwIcon size={16} />, () => void reboot(), true)}
    </div>
  );
};
