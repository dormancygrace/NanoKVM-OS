import { Fragment, lazy, Suspense, useContext, useEffect, useRef, useState } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Alert, Button, Divider, Modal, Spin, Tooltip, type TooltipProps } from 'antd';
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
  StethoscopeIcon,
  UsbIcon,
  UserRoundIcon,
  VideoIcon,
  WifiIcon
} from 'lucide-react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { getAddonInventory, type AddonInventory } from '@/api/addons';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { addonInventoryAtom } from '@/jotai/addons';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { picoclawChatOpenAtom, picoclawRuntimeStatusAtom } from '@/jotai/picoclaw.ts';
import { rustDeskStatusAtom } from '@/jotai/rustdesk.ts';
import { settingsRequestAtom, submenuOpenCountAtom } from '@/jotai/settings.ts';
import { useResponsiveDevice } from '@/hooks/useResponsiveDevice.ts';
import { Netbird as NetbirdIcon } from '@/components/icons/netbird';
import { OpenVPNIcon } from '@/components/icons/openvpn';
import { RustDeskIcon } from '@/components/icons/rustdesk';
import { Tailscale as TailscaleIcon } from '@/components/icons/tailscale';
import { WireGuardIcon } from '@/components/icons/wireguard';
import { MobileMenuItemContext } from '@/components/mobile-menu-context.ts';
import { ScrollArea } from '@/components/ui/scroll-area';

import styles from './sidebar.module.css';
import { resolveSettingsTab, settingsGroupOf, type SettingsGroup } from './tabs.ts';

// Keep the navigation light; only the selected settings page is fetched.
const About = lazy(() => import('./about').then((module) => ({ default: module.About })));
const Account = lazy(() => import('./account').then((module) => ({ default: module.Account })));
const Appearance = lazy(() =>
  import('./appearance').then((module) => ({ default: module.Appearance }))
);
const Dashboard = lazy(() =>
  import('./dashboard').then((module) => ({ default: module.Dashboard }))
);
const DateTimeSettings = lazy(() =>
  import('./date-time').then((module) => ({ default: module.DateTimeSettings }))
);
const MCP = lazy(() => import('./mcp').then((module) => ({ default: module.MCP })));
const Memory = lazy(() => import('./memory').then((module) => ({ default: module.Memory })));
const Netbird = lazy(() => import('./netbird').then((module) => ({ default: module.Netbird })));
const Tailscale = lazy(() =>
  import('./tailscale').then((module) => ({ default: module.Tailscale }))
);
const Updates = lazy(() => import('./updates').then((module) => ({ default: module.Updates })));
const Usb = lazy(() => import('./usb').then((module) => ({ default: module.Usb })));
const VideoSettings = lazy(() =>
  import('./video').then((module) => ({ default: module.VideoSettings }))
);
const WireGuard = lazy(() => import('./vpn').then((module) => ({ default: module.WireGuard })));
const OpenVPN = lazy(() => import('./vpn/openvpn').then((module) => ({ default: module.OpenVPN })));
const System = lazy(() => import('./system').then((module) => ({ default: module.System })));
const Diagnostics = lazy(() =>
  import('./system/diagnostics').then((module) => ({ default: module.Diagnostics }))
);
const Addons = lazy(() =>
  import('./software/addons').then((module) => ({ default: module.Addons }))
);
const RustDeskControls = lazy(() =>
  import('./extensions/rustdesk').then((module) => ({ default: module.RustDeskControls }))
);
const Software = lazy(() => import('./software').then((module) => ({ default: module.Software })));
const Network = lazy(() => import('./network').then((module) => ({ default: module.Network })));
const WifiSettings = lazy(() =>
  import('./network').then((module) => ({ default: module.WifiSettings }))
);
const EthernetSettings = lazy(() =>
  import('./network').then((module) => ({ default: module.EthernetSettings }))
);

const PageLoading = () => (
  <div className="flex justify-center pt-10">
    <Spin size="small" />
  </div>
);

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
  const [expandedGroups, setExpandedGroups] = useState<ReadonlySet<SettingsGroup>>(new Set());
  const [rustDeskStatus] = useAtom(rustDeskStatusAtom);
  const [inventory, setInventory] = useAtom(addonInventoryAtom);
  const [, setInventoryError] = useState(false);
  const [picoclawStatus] = useAtom(picoclawRuntimeStatusAtom);
  const setPicoclawOpen = useSetAtom(picoclawChatOpenAtom);
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
          { id: 'network', icon: <NetworkIcon size={16} />, component: null },
          { id: 'network-general', icon: <SettingsIcon size={16} />, component: <Network /> },
          { id: 'network-wifi', icon: <WifiIcon size={16} />, component: <WifiSettings /> },
          {
            id: 'network-ethernet',
            icon: <EthernetPortIcon size={16} />,
            component: <EthernetSettings />
          },
          {
            id: 'network-openvpn',
            icon: <OpenVPNIcon />,
            component: <OpenVPN setIsLocked={setIsLocked} />
          },
          {
            id: 'network-tailscale',
            icon: <TailscaleIcon />,
            component: <Tailscale setIsLocked={setIsLocked} />
          },
          {
            id: 'network-netbird',
            icon: <NetbirdIcon />,
            component: <Netbird setIsLocked={setIsLocked} />
          },
          {
            id: 'network-wireguard',
            icon: <WireGuardIcon />,
            component: <WireGuard setIsLocked={setIsLocked} />
          },
          { id: 'system', icon: <ShieldIcon size={16} />, component: null },
          { id: 'system-general', icon: <SettingsIcon size={16} />, component: <System /> },
          { id: 'system-users', icon: <UserRoundIcon size={16} />, component: <Account /> },
          {
            id: 'system-date-time',
            icon: <ClockIcon size={16} />,
            component: <DateTimeSettings />
          },
          { id: 'system-updates', icon: <DownloadIcon size={16} />, component: <Updates /> },
          { id: 'system-mcp', icon: <BotIcon size={16} />, component: <MCP /> },
          { id: 'system-memory', icon: <MemoryStickIcon size={16} />, component: <Memory /> },
          {
            id: 'system-diagnostics',
            icon: <StethoscopeIcon size={16} />,
            component: <Diagnostics />
          },
          { id: 'software', icon: <PackageIcon size={16} />, component: null },
          {
            id: 'software-addons',
            icon: <PackageIcon size={16} />,
            component: (
              <Addons
                onOpenRustDesk={() => changeTab('software-rustdesk')}
                onOpen={() => {
                  closeModal();
                  setPicoclawOpen(true);
                }}
              />
            )
          },
          { id: 'software-packages', icon: <PackageIcon size={16} />, component: <Software /> },
          ...(inventory?.rustdesk.installed
            ? [
                {
                  id: 'software-rustdesk',
                  icon: <RustDeskIcon size={16} />,
                  component: <RustDeskControls />
                }
              ]
            : [])
        ]
      : []),
    { id: 'appearance', icon: <PaletteIcon size={16} />, component: <Appearance /> },
    ...(!isAdmin
      ? [{ id: 'account', icon: <UserRoundIcon size={16} />, component: <Account /> }]
      : []),
    { id: 'about', icon: <InfoIcon size={16} />, component: <About /> }
  ];

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
    if (currentTab === 'software-rustdesk' && inventory?.rustdesk.installed === false) {
      setCurrentTab('software-addons');
      setDetailOpen(true);
    }
  }, [currentTab, inventory?.rustdesk.installed]);

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
    const requested = resolveSettingsTab(request);
    expandGroupOf(requested);
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

  function expandGroupOf(id: string) {
    const group = settingsGroupOf(id);
    if (group) setExpandedGroups((groups) => new Set(groups).add(group));
  }

  function toggleGroup(group: SettingsGroup) {
    setExpandedGroups((groups) => {
      const next = new Set(groups);
      if (!next.delete(group)) next.add(group);
      return next;
    });
  }

  function changeTab(tab: string) {
    if (isLocked) {
      return;
    }

    if (tab === 'network' || tab === 'system' || tab === 'software') {
      toggleGroup(tab);
      return;
    }
    // An unknown or no longer available tab falls back to the dashboard
    // instead of rendering an empty page.
    const resolved = resolveSettingsTab(tab);
    const target = tabs.some((item) => item.id === resolved) ? resolved : 'dashboard';
    expandGroupOf(target);
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
    setExpandedGroups(new Set());
    setSubmenuOpenCount((count) => Math.max(0, count - 1));
  }

  function tabTitle(id: string) {
    if (id === 'dashboard') return t('dashboard.title');
    if (id === 'video') return t('videoSettings.title');
    if (id === 'network') return t('settings.network.title');
    if (id === 'network-general') return t('settings.network.general');
    if (id === 'network-wifi') return t('settings.network.wifi.title');
    if (id === 'network-ethernet') return t('settings.network.ethernet.name');
    if (id === 'network-openvpn') return 'OpenVPN';
    if (id === 'network-tailscale') return 'Tailscale';
    if (id === 'network-netbird') return 'NetBird';
    if (id === 'network-wireguard') return 'WireGuard';
    if (id === 'system') return t('settings.system.title');
    if (id === 'system-general') return t('settings.system.general');
    if (id === 'system-users') return t('settings.account.title');
    if (id === 'system-date-time') return t('dateTime.title');
    if (id === 'system-updates') return t('settings.updates.title');
    if (id === 'system-mcp') return t('settings.mcp.title');
    if (id === 'system-memory') return t('settings.memory.title');
    if (id === 'system-diagnostics') return t('settings.system.diagnostics.title');
    if (id === 'software') return t('settings.software.title');
    if (id === 'software-addons') return t('settings.software.addons.title');
    if (id === 'software-packages') return t('settings.software.addons.packages');
    if (id === 'software-rustdesk') return 'RustDesk';
    if (id === 'account') return t('settings.account.title');
    return t(`settings.${id}.title`);
  }

  // "Group / Page" for pages inside a group.
  function pageTitle(id: string) {
    const group = settingsGroupOf(id);
    return group ? `${tabTitle(group)} / ${tabTitle(id)}` : tabTitle(id);
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
            'flex min-w-0 overflow-hidden rounded-lg outline outline-1 outline-neutral-700',
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
                  icon={<ArrowLeftIcon size={16} />}
                  onClick={() => setDetailOpen(false)}
                />
              )}
              <span className="truncate font-medium">
                {detailOpen ? pageTitle(currentTab) : t('settings.title')}
              </span>
            </div>
          )}
          <div
            className={clsx(
              mobile
                ? detailOpen
                  ? 'hidden'
                  : 'min-h-0 flex-1 overflow-x-hidden overflow-y-auto p-2'
                : 'flex h-full max-w-[260px] min-w-0 shrink-0 flex-col space-y-0.5 overflow-x-hidden overflow-y-auto rounded-l-lg bg-neutral-800/90 px-1 sm:w-1/5 md:w-1/4 md:px-2'
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
                  (() => {
                    const group = settingsGroupOf(tab.id);
                    return !group || expandedGroups.has(group);
                  })()
              )
              .map((tab) => {
                const child = settingsGroupOf(tab.id) !== undefined;
                const expanded =
                  tab.id === 'network' || tab.id === 'system' || tab.id === 'software'
                    ? expandedGroups.has(tab.id)
                    : undefined;
                const label = tabTitle(tab.id);
                return (
                  <Fragment key={tab.id}>
                    <button
                      type="button"
                      disabled={isLocked}
                      aria-label={label}
                      aria-current={currentTab === tab.id ? 'page' : undefined}
                      data-child={child || undefined}
                      aria-expanded={expanded}
                      className={clsx(
                        styles.item,
                        'flex items-center gap-2 rounded-lg p-2 text-left select-none sm:px-3',
                        mobile && 'min-h-12',
                        child ? 'ml-4 w-[calc(100%_-_1rem)]' : 'w-full'
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
                  </Fragment>
                );
              })}
            <div className={clsx('px-3 pt-6 pb-4', !mobile && 'mt-auto!')}>
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
              <div className="flex h-full w-full min-w-0 justify-center">
                <div
                  className={clsx(
                    'w-full min-w-0 pb-10',
                    currentTab === 'dashboard' ? 'max-w-[820px]' : 'max-w-[600px]',
                    mobile ? 'pt-5' : 'pt-14'
                  )}
                >
                  <ErrorBoundary
                    key={currentTab}
                    fallback={<Alert type="error" showIcon message={t('error.title')} />}
                  >
                    {!mobile && currentTab !== 'about' && (
                      <>
                        <h2 className="text-fg m-0 text-base font-medium">
                          {pageTitle(currentTab)}
                        </h2>
                        <Divider className="opacity-50" />
                      </>
                    )}
                    <Suspense fallback={<PageLoading />}>
                      {(tabs.find((tab) => tab.id === currentTab) ?? tabs[0]).component}
                    </Suspense>
                  </ErrorBoundary>
                </div>
              </div>
            </ScrollArea>
          )}
        </div>
      </Modal>
    </>
  );
};
