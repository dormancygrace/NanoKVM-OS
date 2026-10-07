import { lazy, Suspense, useContext, useEffect, useRef, useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Tooltip, type TooltipProps } from 'antd';
import { useAtom, useSetAtom } from 'jotai';
import { SettingsIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getAddonInventory, type AddonInventory } from '@/api/addons';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { addonInventoryAtom } from '@/jotai/addons';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { picoclawChatOpenAtom, picoclawRuntimeStatusAtom } from '@/jotai/picoclaw.ts';
import { rustDeskStatusAtom } from '@/jotai/rustdesk.ts';
import { settingsRequestAtom, submenuOpenCountAtom } from '@/jotai/settings.ts';
import { useResponsiveDevice } from '@/hooks/useResponsiveDevice.ts';
import { MobileMenuItemContext } from '@/components/mobile-menu-context.ts';

// The modal body (navigation and page loaders) is only fetched once needed.
const loadDialog = () => import('./dialog.tsx');
const SettingsDialog = lazy(() =>
  loadDialog().then((module) => ({ default: module.SettingsDialog }))
);
const preloadDialog = () => void loadDialog();

export const Settings = ({
  tooltipPlacement = 'bottom'
}: {
  tooltipPlacement?: TooltipProps['placement'];
}) => {
  const { isMobilePortrait: mobile } = useResponsiveDevice();
  const { onRequestMobileMenuDismiss } = useContext(MobileMenuItemContext);
  const [detailOpen, setDetailOpen] = useState(false);
  const modalOpenRef = useRef(false);
  const { t } = useTranslation();
  const { account } = useAuth();
  const isAdmin = account.role === 'admin';

  const [request, setRequest] = useAtom(settingsRequestAtom);
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [isLocked, setIsLocked] = useState(false);
  const [currentTab, setCurrentTab] = useState('dashboard');
  const [vpnExpanded, setVpnExpanded] = useState(false);
  const [networkExpanded, setNetworkExpanded] = useState(false);
  const [systemExpanded, setSystemExpanded] = useState(false);
  const [softwareExpanded, setSoftwareExpanded] = useState(false);
  const [extensionsExpanded, setExtensionsExpanded] = useState(false);
  const [rustDeskStatus] = useAtom(rustDeskStatusAtom);
  const [inventory, setInventory] = useAtom(addonInventoryAtom);
  const [inventoryError, setInventoryError] = useState(false);
  const [picoclawStatus] = useAtom(picoclawRuntimeStatusAtom);
  const setPicoclawOpen = useSetAtom(picoclawChatOpenAtom);
  // Mount the dialog on first open and keep it, so closing still animates.
  const [dialogWanted, setDialogWanted] = useState(false);
  if (isModalOpen && !dialogWanted) setDialogWanted(true);

  const setKeyboardLock = useSetAtom(keyboardLockAtom);
  const setSubmenuOpenCount = useSetAtom(submenuOpenCountAtom);

  useEffect(() => {
    if (!isAdmin) return;
    let active = true;
    let pending = false;
    const refresh = async () => {
      if (pending) return;
      pending = true;
      try {
        const response = await getAddonInventory();
        if (!active) return;
        if (response.code !== 0) throw new Error(response.msg);
        setInventory(response.data as AddonInventory);
        setInventoryError(false);
      } catch {
        if (active) setInventoryError(true);
      } finally {
        pending = false;
      }
    };
    // Start before the modal opens, and refresh installation facts while open.
    void refresh();
    const stop = isModalOpen ? pollWhileVisible(() => void refresh(), 15000) : undefined;
    return () => {
      active = false;
      stop?.();
    };
  }, [isAdmin, isModalOpen, setInventory]);

  useEffect(() => {
    if (rustDeskStatus) {
      setInventory(
        (current) =>
          current && {
            ...current,
            rustdesk: { installed: rustDeskStatus.installed }
          }
      );
    }
  }, [rustDeskStatus, setInventory]);

  useEffect(() => {
    if (picoclawStatus) {
      setInventory(
        (current) =>
          current && {
            ...current,
            picoclaw: { installed: picoclawStatus.installed }
          }
      );
    }
  }, [picoclawStatus, setInventory]);

  useEffect(() => {
    if (currentTab === 'extensions-rustdesk' && inventory?.rustdesk.installed === false) {
      setCurrentTab('software-addons');
      setSoftwareExpanded(true);
      setDetailOpen(true);
    }
  }, [currentTab, inventory?.rustdesk.installed]);

  useEffect(
    () => () => {
      if (!modalOpenRef.current) return;
      modalOpenRef.current = false;
      setKeyboardLock({ source: 'settings-modal', locked: false });
      setSubmenuOpenCount((count) => Math.max(0, count - 1));
    },
    [setKeyboardLock, setSubmenuOpenCount]
  );

  useEffect(() => {
    if (!request || isLocked) return;
    const requested =
      request === 'tailscale' || request === 'vpn'
        ? 'vpn-tailscale'
        : request === 'network'
          ? 'network-general'
          : request === 'system'
            ? 'system-general'
            : request === 'system-software'
              ? 'software-packages'
              : request;
    if (requested.startsWith('vpn-')) setVpnExpanded(true);
    if (requested.startsWith('network-')) setNetworkExpanded(true);
    if (requested.startsWith('software-')) setSoftwareExpanded(true);
    if (requested.startsWith('extensions-')) setExtensionsExpanded(true);
    if (requested.startsWith('system-')) setSystemExpanded(true);
    setCurrentTab(requested);
    setDetailOpen(true);
    if (!modalOpenRef.current) {
      modalOpenRef.current = true;
      setIsModalOpen(true);
      setKeyboardLock({ source: 'settings-modal', locked: true });
      setSubmenuOpenCount((count) => count + 1);
    }
    setRequest(null);
  }, [request, isLocked, isModalOpen, setRequest, setKeyboardLock, setSubmenuOpenCount]);

  function changeTab(tab: string) {
    if (isLocked) {
      return;
    }

    if (tab === 'vpn') {
      setVpnExpanded((expanded) => !expanded);
      return;
    }
    if (tab === 'network') {
      setNetworkExpanded((expanded) => !expanded);
      return;
    }
    if (tab === 'software') {
      setSoftwareExpanded((expanded) => !expanded);
      return;
    }
    if (tab === 'extensions') {
      setExtensionsExpanded((expanded) => !expanded);
      return;
    }
    if (tab === 'extensions-picoclaw') {
      closeModal();
      setPicoclawOpen(true);
      return;
    }
    if (tab === 'system') {
      setSystemExpanded((expanded) => !expanded);
      return;
    }
    const target = tab;
    if (target.startsWith('vpn-')) setVpnExpanded(true);
    if (target.startsWith('network-')) setNetworkExpanded(true);
    if (target.startsWith('software-')) setSoftwareExpanded(true);
    if (target.startsWith('extensions-')) setExtensionsExpanded(true);
    if (target.startsWith('system-')) setSystemExpanded(true);
    setCurrentTab(target);
    setDetailOpen(true);
  }

  function openModal() {
    if (modalOpenRef.current) return;
    modalOpenRef.current = true;
    onRequestMobileMenuDismiss?.();
    setCurrentTab('dashboard');
    setDetailOpen(!mobile);
    setIsModalOpen(true);
    setKeyboardLock({ source: 'settings-modal', locked: true });
    setSubmenuOpenCount((count) => count + 1);
  }

  function closeModal() {
    if (isLocked) {
      return;
    }

    if (!modalOpenRef.current) return;
    modalOpenRef.current = false;
    setKeyboardLock({ source: 'settings-modal', locked: false });
    setIsModalOpen(false);
    setCurrentTab('dashboard');
    setVpnExpanded(false);
    setNetworkExpanded(false);
    setSystemExpanded(false);
    setSoftwareExpanded(false);
    setExtensionsExpanded(false);
    setSubmenuOpenCount((count) => Math.max(0, count - 1));
  }

  return (
    <>
      <Tooltip
        title={t('settings.title')}
        placement={tooltipPlacement}
        mouseEnterDelay={0.6}
        open={mobile ? false : undefined}
      >
        <div
          role="button"
          aria-label={t('settings.title')}
          tabIndex={0}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              openModal();
            }
          }}
          className="flex h-[30px] w-[30px] cursor-pointer items-center justify-center rounded hover:bg-neutral-700/80"
          onClick={openModal}
          onPointerEnter={preloadDialog}
          onFocus={preloadDialog}
        >
          <div className="flex items-center justify-center text-neutral-300 hover:text-white">
            <SettingsIcon size={18} className="block" />
          </div>
        </div>
      </Tooltip>

      {dialogWanted && (
        <Suspense fallback={null}>
          <SettingsDialog
            isModalOpen={isModalOpen}
            mobile={mobile}
            isAdmin={isAdmin}
            isLocked={isLocked}
            setIsLocked={setIsLocked}
            currentTab={currentTab}
            detailOpen={detailOpen}
            setDetailOpen={setDetailOpen}
            vpnExpanded={vpnExpanded}
            networkExpanded={networkExpanded}
            systemExpanded={systemExpanded}
            softwareExpanded={softwareExpanded}
            extensionsExpanded={extensionsExpanded}
            inventory={inventory}
            inventoryError={inventoryError}
            changeTab={changeTab}
            closeModal={closeModal}
            openPicoclaw={() => {
              closeModal();
              setPicoclawOpen(true);
            }}
          />
        </Suspense>
      )}
    </>
  );
};
