import { useContext, useEffect, useRef, useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button, Modal, Tooltip, type TooltipProps } from 'antd';
import clsx from 'clsx';
import { useAtom, useSetAtom } from 'jotai';
import {
  ArrowLeftIcon,
  BotIcon,
  ChevronRightIcon,
  ClockIcon,
  DownloadIcon,
  EthernetPortIcon,
  InfoIcon,
  LayoutDashboardIcon,
  MemoryStickIcon,
  NetworkIcon,
  PackageIcon,
  PaletteIcon,
  SettingsIcon,
  ShieldIcon,
  SmartphoneIcon,
  UsbIcon,
  UserRoundIcon,
  VideoIcon,
  WifiIcon
} from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { settingsRequestAtom, submenuOpenCountAtom } from '@/jotai/settings.ts';
import { useResponsiveDevice } from '@/hooks/useResponsiveDevice.ts';
import { Netbird as NetbirdIcon } from '@/components/icons/netbird';
import { OpenVPNIcon } from '@/components/icons/openvpn';
import { Tailscale as TailscaleIcon } from '@/components/icons/tailscale';
import { WireGuardIcon } from '@/components/icons/wireguard';
import { MobileMenuItemContext } from '@/components/mobile-menu-context.ts';
import { ScrollArea } from '@/components/ui/scroll-area';

import { About } from './about';
import { Account } from './account';
import { Appearance } from './appearance';
import { Dashboard } from './dashboard';
import { DateTimeSettings } from './date-time';
import { Device } from './device';
import { MCP } from './mcp';
import { Memory } from './memory';
import { EthernetSettings, Network, WifiSettings } from './network';
import styles from './sidebar.module.css';
import { Netbird } from './netbird';
import { Tailscale } from './tailscale';
import { Updates } from './updates';
import { Usb } from './usb';
import { VideoSettings } from './video';
import { WireGuard } from './vpn';
import { OpenVPN } from './vpn/openvpn';
import { System } from './system';
import { Software } from './software';

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
  const scrollViewportRef = useRef<HTMLDivElement>(null);

  const setKeyboardLock = useSetAtom(keyboardLockAtom);
  const setSubmenuOpenCount = useSetAtom(submenuOpenCountAtom);

  const tabs = [
    {
      id: 'dashboard',
      icon: <LayoutDashboardIcon size={16} />,
      component: <Dashboard navigate={changeTab} />
    },
    {
      id: 'video',
      icon: <VideoIcon size={16} />,
      component: <VideoSettings setIsLocked={setIsLocked} />
    },
    ...(isAdmin
      ? [
          { id: 'usb', icon: <UsbIcon size={16} />, component: <Usb /> },
          { id: 'device', icon: <SmartphoneIcon size={16} />, component: <Device /> },
          { id: 'network', icon: <NetworkIcon size={16} />, component: null },
          { id: 'network-general', icon: <SettingsIcon size={16} />, component: <Network /> },
          { id: 'network-wifi', icon: <WifiIcon size={16} />, component: <WifiSettings /> },
          {
            id: 'network-ethernet',
            icon: <EthernetPortIcon size={16} />,
            component: <EthernetSettings />
          },
          { id: 'system', icon: <SettingsIcon size={16} />, component: null },
          { id: 'system-general', icon: <SettingsIcon size={16} />, component: <System /> },
          { id: 'system-memory', icon: <MemoryStickIcon size={16} />, component: <Memory /> },
          { id: 'system-date-time', icon: <ClockIcon size={16} />, component: <DateTimeSettings /> },
          { id: 'system-users', icon: <UserRoundIcon size={16} />, component: <Account /> },
          { id: 'system-mcp', icon: <BotIcon size={16} />, component: <MCP /> },
          { id: 'system-updates', icon: <DownloadIcon size={16} />, component: <Updates /> },
          { id: 'system-software', icon: <PackageIcon size={16} />, component: <Software /> },
          {
            id: 'vpn',
            icon: <ShieldIcon size={16} />,
            component: null
          },
          {
            id: 'vpn-openvpn',
            icon: <OpenVPNIcon />,
            component: <OpenVPN setIsLocked={setIsLocked} />
          },
          {
            id: 'vpn-tailscale',
            icon: <TailscaleIcon />,
            component: <Tailscale setIsLocked={setIsLocked} />
          },
          {
            id: 'vpn-netbird',
            icon: <NetbirdIcon />,
            component: <Netbird setIsLocked={setIsLocked} />
          },
          {
            id: 'vpn-wireguard',
            icon: <WireGuardIcon />,
            component: <WireGuard setIsLocked={setIsLocked} />
          },
        ]
      : []),
    { id: 'appearance', icon: <PaletteIcon size={16} />, component: <Appearance /> },
    ...(!isAdmin ? [{ id: 'account', icon: <UserRoundIcon size={18} />, component: <Account /> }] : []),
    { id: 'about', icon: <InfoIcon size={14} />, component: <About /> }
  ];

  useEffect(() => {
    scrollViewportRef.current?.scrollTo({ top: 0, left: 0 });
  }, [currentTab]);

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
          : request;
    if (requested.startsWith('vpn-')) setVpnExpanded(true);
    if (requested.startsWith('network-')) setNetworkExpanded(true);
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
    if (tab === 'system') {
      setSystemExpanded((expanded) => !expanded);
      return;
    }
    const target = tab;
    if (target.startsWith('vpn-')) setVpnExpanded(true);
    if (target.startsWith('network-')) setNetworkExpanded(true);
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
    setSubmenuOpenCount((count) => Math.max(0, count - 1));
  }

  function tabTitle(id: string) {
    if (id === 'dashboard') return 'Dashboard';
    if (id === 'system-date-time') return t('dateTime.title');
    if (id === 'video') return t('videoSettings.title');
    if (id === 'network') return t('settings.network.title');
    if (id === 'network-wifi') return t('settings.network.wifi.title');
    if (id === 'network-ethernet') return 'Ethernet';
    if (id === 'network-general') return t('settings.network.general');
    if (id === 'system') return t('settings.system.title');
    if (id === 'system-general') return t('settings.system.general');
    if (id === 'system-memory') return t('settings.memory.title');
    if (id === 'system-users') return t('settings.account.title');
    if (id === 'system-mcp') return t('settings.mcp.title');
    if (id === 'system-updates') return t('settings.updates.title');
    if (id === 'system-software') return t('settings.software.title');
    if (id === 'account') return t('settings.account.title');
    if (id === 'vpn') return 'VPN';
    if (id === 'vpn-tailscale') return 'Tailscale';
    if (id === 'vpn-netbird') return 'NetBird';
    if (id === 'vpn-wireguard') return 'WireGuard';
    if (id === 'vpn-openvpn') return 'OpenVPN';
    return t(`settings.${id}.title`);
  }

  return (
    <>
      <Tooltip title={t('settings.title')} placement={tooltipPlacement} mouseEnterDelay={0.6} open={mobile ? false : undefined}>
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
        >
          <div className="flex items-center justify-center text-neutral-300 hover:text-white">
            <SettingsIcon size={18} className="block" />
          </div>
        </div>
      </Tooltip>

      <Modal
        open={isModalOpen}
        width={mobile ? 'calc(100% - 16px)' : '80%'}
        centered={true}
        footer={null}
        destroyOnHidden={true}
        onCancel={closeModal}
        style={{ maxWidth: '1080px' }}
        styles={{ container: { padding: 0 } }}
      >
        <div
          className={clsx(
            'flex min-w-0 rounded-lg outline outline-1 outline-neutral-700',
            mobile ? 'h-[calc(100dvh-32px)] flex-col overflow-hidden' : 'h-[80vh] max-h-[700px]'
          )}
        >
          {mobile && (
            <div className="flex h-12 shrink-0 items-center gap-2 border-b border-neutral-700 bg-neutral-800 px-3 pr-12">
              {detailOpen && (
                <Button
                  type="text"
                  aria-label={t('settings.back')}
                  disabled={isLocked}
                  icon={<ArrowLeftIcon size={18} />}
                  onClick={() => setDetailOpen(false)}
                />
              )}
              <span className="truncate font-medium">
                {detailOpen ? tabTitle(currentTab) : t('settings.title')}
              </span>
            </div>
          )}
          <div
            className={clsx(
              mobile
                ? detailOpen
                  ? 'hidden'
                  : 'min-h-0 flex-1 overflow-y-auto p-2'
                : 'flex h-full max-w-[260px] flex-col space-y-0.5 overflow-y-auto rounded-l-lg bg-neutral-800/90 px-1 sm:w-1/5 md:w-1/4 md:px-2'
            )}
          >
            <div className={mobile ? 'hidden' : 'hidden px-3 pt-10 text-xl sm:block'}>
              {t('settings.title')}
            </div>
            <div className={mobile ? 'hidden' : 'h-10 sm:h-5'} />
            {tabs
              .filter(
                (tab) =>
                  tab.id !== 'about' &&
                  (!tab.id.startsWith('vpn-') || vpnExpanded) &&
                  (!tab.id.startsWith('network-') || networkExpanded) &&
                  (!tab.id.startsWith('system-') || systemExpanded)
              )
              .map((tab) => {
                const child =
                  tab.id.startsWith('vpn-') ||
                  tab.id.startsWith('network-') ||
                  tab.id.startsWith('system-');
                const expanded =
                  tab.id === 'vpn'
                    ? vpnExpanded
                    : tab.id === 'network'
                      ? networkExpanded
                      : tab.id === 'system'
                        ? systemExpanded
                        : undefined;
                const label = tabTitle(tab.id);
                return (
                  <button
                    type="button"
                    key={tab.id}
                    disabled={isLocked}
                    aria-label={label}
                    aria-current={currentTab === tab.id ? 'page' : undefined}
                    data-child={child || undefined}
                    aria-expanded={expanded}
                    className={clsx(
                      styles.item,
                      'flex w-full select-none items-center gap-2 rounded-lg p-2 text-left sm:px-3',
                      mobile && 'min-h-12',
                      child && 'ml-4 w-[calc(100%-1rem)]'
                    )}
                    onClick={() => changeTab(tab.id)}
                  >
                    <div className="flex h-[18px] w-[18px] shrink-0 items-center justify-center">
                      {tab.icon}
                    </div>
                    <span
                      className={mobile ? 'truncate text-sm' : 'hidden truncate text-sm sm:block'}
                    >
                      {label}
                    </span>
                    {expanded !== undefined && (
                      <ChevronRightIcon
                        size={12}
                        className={clsx(
                          'ml-auto shrink-0 transition-transform',
                          !mobile && 'hidden sm:block',
                          expanded && 'rotate-90'
                        )}
                      />
                    )}
                  </button>
                );
              })}
            <div className={clsx('px-3 pb-4 pt-6', !mobile && 'mt-auto!')}>
              <button
                type="button"
                disabled={isLocked}
                aria-label={tabTitle('about')}
                aria-current={currentTab === 'about' ? 'page' : undefined}
                className={clsx(
                  styles.about,
                  'inline-flex items-center gap-2 rounded py-2 text-xs text-neutral-400 hover:text-neutral-100 focus-visible:outline focus-visible:outline-2 focus-visible:outline-blue-400',
                  mobile && 'min-h-11',
                  currentTab === 'about' && 'text-neutral-100'
                )}
                onClick={() => changeTab('about')}
              >
                <InfoIcon size={14} />
                {tabTitle('about')}
              </button>
            </div>
          </div>

          {(!mobile || detailOpen) && (
            <ScrollArea
              viewportRef={scrollViewportRef}
              className="box-border min-h-0 min-w-0 flex-1 rounded-r-lg bg-neutral-900/50 px-3 [&_[data-slot=scroll-area-scrollbar]]:w-1.5 [&_[data-slot=scroll-area-scrollbar]]:p-0 [&_[data-slot=scroll-area-thumb]]:bg-neutral-500/30"
            >
              <div
                className={clsx(
                  'flex h-full w-full min-w-0 justify-center',
                  mobile && 'nanokvm-settings-mobile-content'
                )}
              >
                <div
                  className={clsx(
                    'w-full min-w-0 pb-10',
                    currentTab === 'dashboard' ? 'max-w-[820px]' : 'max-w-[600px]',
                    mobile ? 'pt-5' : 'pt-14'
                  )}
                >
                  <>{tabs.find((tab) => tab.id === currentTab)?.component}</>
                </div>
              </div>
            </ScrollArea>
          )}
        </div>
      </Modal>
    </>
  );
};
