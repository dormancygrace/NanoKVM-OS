import {
  Fragment,
  Suspense,
  useContext,
  useEffect,
  useRef,
  useState,
  type ComponentType,
  type ReactNode
} from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Alert, Button, Divider, Modal, Spin, type TooltipProps } from 'antd';
import clsx from 'clsx';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import {
  ArrowLeftIcon,
  BatteryChargingIcon,
  BellIcon,
  BluetoothIcon,
  BotIcon,
  CastIcon,
  ChevronRightIcon,
  ClockIcon,
  DownloadIcon,
  EthernetPortIcon,
  FingerprintIcon,
  GlobeLockIcon,
  InfoIcon,
  LayersIcon,
  LayoutDashboardIcon,
  LockIcon,
  MemoryStickIcon,
  MessageSquareIcon,
  NetworkIcon,
  PackageIcon,
  PaletteIcon,
  RadioTowerIcon,
  ScanTextIcon,
  ServerCogIcon,
  ServerIcon,
  SettingsIcon,
  ShieldIcon,
  SparklesIcon,
  StampIcon,
  StethoscopeIcon,
  ThermometerIcon,
  UsbIcon,
  UserRoundIcon,
  VideoIcon,
  WifiIcon
} from 'lucide-react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { getWiFi } from '@/api/network.ts';
import { getVirtualDevice } from '@/api/virtual-device.ts';
import { getBluetoothState } from '@/api/vm.ts';
import { releasePointerMenuFocus } from '@/lib/menu-focus.ts';
import { retryableLazy } from '@/lib/retryable-lazy.ts';
import { keyboardLockAtom } from '@/jotai/keyboard.ts';
import { picoclawChatOpenAtom } from '@/jotai/picoclaw.ts';
import { settingsRequestAtom, submenuOpenCountAtom } from '@/jotai/settings.ts';
import { updateNoticeAtom } from '@/jotai/updates.ts';
import { useResponsiveDevice } from '@/hooks/useResponsiveDevice.ts';
import { RustDeskIcon } from '@/components/icons/rustdesk';
import { MenuTooltip } from '@/components/menu-tooltip.tsx';
import { MobileMenuItemContext } from '@/components/mobile-menu-context.ts';
import { ScrollArea } from '@/components/ui/scroll-area';

import { HostSensorsPage } from './host-sensors/index.tsx';
import { builtinMenu, orderMenu, type BuiltinMenuId, type SettingsMenuEntry } from './menu.ts';
import { SessionActions } from './session-actions.tsx';
import styles from './sidebar.module.css';
import {
  isSettingsGroup,
  resolveSettingsTab,
  settingsCollectionOf,
  settingsGroupOf,
  settingsParentOf,
  type SettingsGroup
} from './tabs.ts';

const retryPages: (() => void)[] = [];
function lazyPage<T extends ComponentType<any>>(load: () => Promise<{ default: T }>) {
  const page = retryableLazy(load);
  retryPages.push(page.retry);
  return page.Component;
}

// Keep the navigation light; only the selected settings page is fetched.
const About = lazyPage(() => import('./about').then((module) => ({ default: module.About })));
const Account = lazyPage(() => import('./account').then((module) => ({ default: module.Account })));
const Appearance = lazyPage(() =>
  import('./appearance').then((module) => ({ default: module.Appearance }))
);
const PicoclawSettingsPage = lazyPage(() =>
  import('./ai/picoclaw.tsx').then((module) => ({ default: module.PicoclawSettingsPage }))
);
const ScreenTextSettingsPage = lazyPage(() =>
  import('./ai/screen-text.tsx').then((module) => ({ default: module.ScreenTextSettingsPage }))
);
const Dashboard = lazyPage(() =>
  import('./dashboard').then((module) => ({ default: module.Dashboard }))
);
const DateTimeSettings = lazyPage(() =>
  import('./date-time').then((module) => ({ default: module.DateTimeSettings }))
);
const MCP = lazyPage(() => import('./mcp').then((module) => ({ default: module.MCP })));
const Memory = lazyPage(() => import('./memory').then((module) => ({ default: module.Memory })));
const Netbird = lazyPage(() => import('./netbird').then((module) => ({ default: module.Netbird })));
const Zerotier = lazyPage(() =>
  import('./zerotier').then((module) => ({ default: module.Zerotier }))
);
const Notifications = lazyPage(() =>
  import('./notifications').then((module) => ({ default: module.Notifications }))
);
const Tailscale = lazyPage(() =>
  import('./tailscale').then((module) => ({ default: module.Tailscale }))
);
const Recovery = lazyPage(() =>
  import('./recovery').then((module) => ({ default: module.Recovery }))
);
const Updates = lazyPage(() => import('./updates').then((module) => ({ default: module.Updates })));
const Usb = lazyPage(() => import('./usb').then((module) => ({ default: module.Usb })));
const VideoSettings = lazyPage(() =>
  import('./video').then((module) => ({ default: module.VideoSettings }))
);
const WatermarkPage = lazyPage(() =>
  import('./video/watermark.tsx').then((module) => ({ default: module.WatermarkPage }))
);
const RtspPage = lazyPage(() =>
  import('./services/rtsp.tsx').then((module) => ({ default: module.RtspPage }))
);
const VPNList = lazyPage(() =>
  import('./vpn/list').then((module) => ({ default: module.VPNList }))
);
const WireGuard = lazyPage(() => import('./vpn').then((module) => ({ default: module.WireGuard })));
const OpenVPN = lazyPage(() =>
  import('./vpn/openvpn').then((module) => ({ default: module.OpenVPN }))
);
const System = lazyPage(() => import('./system').then((module) => ({ default: module.System })));
const Security = lazyPage(() =>
  import('./security').then((module) => ({ default: module.Security }))
);
const Events = lazyPage(() =>
  import('./system/events').then((module) => ({ default: module.Events }))
);
const Diagnostics = lazyPage(() =>
  import('./system/diagnostics').then((module) => ({ default: module.Diagnostics }))
);
const RustDeskControls = lazyPage(() =>
  import('./extensions/rustdesk').then((module) => ({ default: module.RustDeskControls }))
);
const BmcSettingsPage = lazyPage(() =>
  import('./system/bmc').then((module) => ({ default: module.BmcSettingsPage }))
);
const ScheduledTasks = lazyPage(() =>
  import('./schedule').then((module) => ({ default: module.ScheduledTasks }))
);
const LldpPage = lazyPage(() =>
  import('./services/lldp').then((module) => ({ default: module.LldpPage }))
);
const SnmpPage = lazyPage(() =>
  import('./services/snmp').then((module) => ({ default: module.SnmpPage }))
);
const UpsPage = lazyPage(() =>
  import('./services/ups').then((module) => ({ default: module.UpsPage }))
);
const Software = lazyPage(() =>
  import('./software').then((module) => ({ default: module.Software }))
);
const Network = lazyPage(() => import('./network').then((module) => ({ default: module.Network })));
const WifiSettings = lazyPage(() =>
  import('./network').then((module) => ({ default: module.WifiSettings }))
);
const EthernetSettings = lazyPage(() =>
  import('./network').then((module) => ({ default: module.EthernetSettings }))
);
const BluetoothSettings = lazyPage(() =>
  import('./network').then((module) => ({ default: module.BluetoothSettings }))
);
const WebRTCSettings = lazyPage(() =>
  import('./network').then((module) => ({ default: module.WebRTCSettings }))
);

// Marks the settings button and System > Updates while updates are available.
const UpdateDot = () => (
  <span
    aria-hidden
    className="absolute -top-0.5 -right-0.5 h-2 w-2 rounded-full bg-blue-500 ring-2 ring-neutral-800"
  />
);

// The icon names menu entries can use.
const menuIcons: Record<string, ReactNode> = {
  battery: <BatteryChargingIcon size={16} />,
  bell: <BellIcon size={16} />,
  bluetooth: <BluetoothIcon size={16} />,
  bot: <BotIcon size={16} />,
  cast: <CastIcon size={16} />,
  clock: <ClockIcon size={16} />,
  dashboard: <LayoutDashboardIcon size={16} />,
  download: <DownloadIcon size={16} />,
  ethernet: <EthernetPortIcon size={16} />,
  fingerprint: <FingerprintIcon size={16} />,
  info: <InfoIcon size={16} />,
  layers: <LayersIcon size={16} />,
  lock: <LockIcon size={16} />,
  memory: <MemoryStickIcon size={16} />,
  message: <MessageSquareIcon size={16} />,
  network: <NetworkIcon size={16} />,
  package: <PackageIcon size={16} />,
  palette: <PaletteIcon size={16} />,
  'radio-tower': <RadioTowerIcon size={16} />,
  rustdesk: <RustDeskIcon size={16} />,
  'scan-text': <ScanTextIcon size={16} />,
  server: <ServerIcon size={16} />,
  'server-cog': <ServerCogIcon size={16} />,
  thermometer: <ThermometerIcon size={16} />,
  settings: <SettingsIcon size={16} />,
  shield: <ShieldIcon size={16} />,
  sparkles: <SparklesIcon size={16} />,
  stamp: <StampIcon size={16} />,
  stethoscope: <StethoscopeIcon size={16} />,
  usb: <UsbIcon size={16} />,
  user: <UserRoundIcon size={16} />,
  video: <VideoIcon size={16} />,
  vpn: <GlobeLockIcon size={16} />,
  wifi: <WifiIcon size={16} />
};

const menuTitles = new Map<string, SettingsMenuEntry['title']>(
  builtinMenu.map((entry) => [entry.id, entry.title])
);

const PageLoading = () => (
  <div className="flex justify-center pt-10">
    <Spin size="small" />
  </div>
);

// The settings page open in this browser tab, so that a reload opens it
// again. sessionStorage lives as long as the tab: a new tab starts closed.
const openTabKey = 'nanokvm-settings-open-tab';
const readOpenTab = () => {
  try {
    return sessionStorage.getItem(openTabKey);
  } catch {
    return null;
  }
};
const writeOpenTab = (tab: string | null) => {
  try {
    if (tab) sessionStorage.setItem(openTabKey, tab);
    else sessionStorage.removeItem(openTabKey);
  } catch {
    // Storage unavailable: a reload starts with the settings closed.
  }
};
// Read once per page load: the menu renders Settings in more than one place.
let reopenTab = readOpenTab();

// Settings shown as a page instead of the dialog: the open tab is part of the
// address, and leaving goes to another page.
export type SettingsPageHost = {
  tab?: string;
  onTab: (tab: string) => void;
  // to the overview
  onClose: () => void;
  // to the remote screen, where the chat lives
  onOpenScreen: () => void;
  // The address names a page this account or this device does not have.
  onUnavailable: () => void;
  // A page is at work (an installation, a connection) and must stay: the
  // host keeps every way of leaving it closed until it is done.
  onLock: (locked: boolean) => void;
  // The host lays the settings out: it gets the navigation and the open page.
  render: (frame: SettingsFrame) => ReactNode;
};

export type SettingsFrameEntry = {
  id: string;
  title: string;
  icon: ReactNode;
  // the group whose entry expands to show this one
  group?: SettingsGroup;
  // a group's own entry: it has no page
  isGroup: boolean;
  // a collection item: opened from its list, not shown in the navigation
  hidden: boolean;
  // marked while updates are available
  marked: boolean;
};

// What the settings give a host that shows them as pages: the entries this
// device and this user have, in menu order, and the page that is open.
export type SettingsFrame = {
  entries: SettingsFrameEntry[];
  // The open page. With no page in the address it is one the host does not
  // show.
  current: string;
  // the list a collection item goes back to
  collection?: string;
  locked: boolean;
  expanded: ReadonlySet<SettingsGroup>;
  // opens a page, or folds and unfolds a group
  open: (id: string) => void;
  title: (id: string) => string;
  pageTitle: (id: string) => string;
  // the open page itself
  content: ReactNode;
};

// The tab a settings page opens without one in its address, or with one that
// names no page.
const pageDefaultTab = 'system-general';
const pageTabOf = (tab: string | undefined) => {
  const resolved = resolveSettingsTab(tab ?? pageDefaultTab);
  return menuTitles.has(resolved) ? resolved : pageDefaultTab;
};

export const Settings = ({
  tooltipPlacement = 'bottom',
  page
}: {
  tooltipPlacement?: TooltipProps['placement'];
  page?: SettingsPageHost;
}) => {
  const { isMobilePortrait: mobile } = useResponsiveDevice();
  const { onRequestMobileMenuDismiss } = useContext(MobileMenuItemContext);
  const [detailOpen, setDetailOpen] = useState(false);
  const modalOpenRef = useRef(false);
  const { t } = useTranslation();
  const { account } = useAuth();
  const isAdmin = account.role === 'admin';
  const updateNotice = useAtomValue(updateNoticeAtom);
  const updatesAvailable = isAdmin && !!updateNotice?.enabled && updateNotice.updates.length > 0;

  const [request, setRequest] = useAtom(settingsRequestAtom);
  const [isModalOpen, setIsModalOpen] = useState(false);
  // a page is always open
  const open = page ? true : isModalOpen;
  const [isLocked, setIsLocked] = useState(false);
  const [currentTab, setCurrentTab] = useState(() => (page ? pageTabOf(page.tab) : 'dashboard'));
  // Network > Bluetooth is listed only where the Wi-Fi chip has Bluetooth.
  const [hasBluetooth, setHasBluetooth] = useState(false);
  const [bluetoothKnown, setBluetoothKnown] = useState(false);
  // Network > Wi-Fi is left out only once the device reports no adapter:
  // until then (and when the request fails) it stays, so a link to it works.
  const [hasWifi, setHasWifi] = useState(true);
  // USB > Identity belongs to the gadget: it is left out while the port is
  // a host or switched off. Until the device answers it stays.
  const [usbGadget, setUsbGadget] = useState(true);

  useEffect(() => {
    if (!open || !isAdmin) return;
    let active = true;
    getBluetoothState()
      .then((rsp) => {
        if (active && rsp.code === 0) setHasBluetooth(!!rsp.data?.available);
      })
      .catch(() => {})
      .finally(() => {
        if (active) setBluetoothKnown(true);
      });
    getWiFi()
      .then((rsp) => {
        if (active && rsp.code === 0) setHasWifi(rsp.data?.supported !== false);
      })
      .catch(() => {});
    const readUsbRole = () =>
      getVirtualDevice()
        .then((rsp) => {
          if (active && rsp.code === 0) setUsbGadget((rsp.data?.role ?? 'gadget') === 'gadget');
        })
        .catch(() => {});
    void readUsbRole();
    window.addEventListener('nanokvm:usb-updated', readUsbRole);
    return () => {
      active = false;
      window.removeEventListener('nanokvm:usb-updated', readUsbRole);
    };
  }, [open, isAdmin]);
  const [expandedGroups, setExpandedGroups] = useState<ReadonlySet<SettingsGroup>>(() => {
    const group = page?.tab !== undefined ? settingsGroupOf(currentTab) : undefined;
    return new Set(group ? [group] : []);
  });
  const setPicoclawOpen = useSetAtom(picoclawChatOpenAtom);
  const scrollViewportRef = useRef<HTMLDivElement>(null);

  const setKeyboardLock = useSetAtom(keyboardLockAtom);
  const setSubmenuOpenCount = useSetAtom(submenuOpenCountAtom);

  // What each menu entry opens. An entry without a page here (not an admin,
  // no Wi-Fi adapter or Bluetooth chip) is not shown; a group (null)
  // shows only with something inside.
  // Typed object literals: a misspelt id is a type error, also here.
  const adminPages: Partial<Record<BuiltinMenuId, ReactNode>> = isAdmin
    ? {
        'video-watermark': <WatermarkPage />,
        usb: null,
        'usb-composition': <Usb part="composition" />,
        'usb-identity': usbGadget ? <Usb part="identity" /> : undefined,
        network: null,
        'network-general': <Network />,
        'network-wifi': hasWifi ? <WifiSettings /> : undefined,
        'network-ethernet': <EthernetSettings />,
        'network-bluetooth': hasBluetooth ? <BluetoothSettings /> : undefined,
        'network-webrtc': <WebRTCSettings />,
        vpn: <VPNList open={changeTab} />,
        'vpn-wireguard': <WireGuard setIsLocked={setIsLocked} />,
        'vpn-openvpn': <OpenVPN setIsLocked={setIsLocked} />,
        'vpn-tailscale': <Tailscale setIsLocked={setIsLocked} />,
        'vpn-netbird': <Netbird setIsLocked={setIsLocked} />,
        'vpn-zerotier': <Zerotier setIsLocked={setIsLocked} />,
        services: null,
        'services-bmc': <BmcSettingsPage />,
        'services-rustdesk': <RustDeskControls />,
        'services-rtsp': <RtspPage />,
        'services-host-sensors': <HostSensorsPage />,
        'services-notifications': <Notifications />,
        'services-schedule': <ScheduledTasks />,
        'services-lldp': <LldpPage />,
        'services-snmp': <SnmpPage />,
        'services-ups': <UpsPage />,
        system: null,
        'system-general': <System />,
        'system-users': <Account />,
        'system-security': <Security onOpenWatermark={() => changeTab('video-watermark')} />,
        'system-date-time': <DateTimeSettings />,
        'system-recovery': <Recovery />,
        'system-updates': <Updates />,
        'system-memory': <Memory />,
        'system-events': <Events />,
        'system-diagnostics': <Diagnostics />,
        ai: null,
        'ai-screen-text': <ScreenTextSettingsPage />,
        'ai-picoclaw': <PicoclawSettingsPage onOpenChat={openChat} />,
        'ai-mcp': <MCP />,
        software: null,
        'software-packages': <Software />
      }
    : {};
  const pages: Partial<Record<BuiltinMenuId, ReactNode>> = {
    dashboard: <Dashboard navigate={changeTab} />,
    video: null,
    'video-general': <VideoSettings setIsLocked={setIsLocked} />,
    appearance: <Appearance />,
    account: isAdmin ? undefined : <Account />,
    about: <About />,
    ...adminPages
  };
  const pageOf = (id: string): ReactNode | undefined => pages[id as BuiltinMenuId];
  const available = orderMenu(builtinMenu).filter((entry) => pageOf(entry.id) !== undefined);
  const tabs = available
    .filter(
      (entry) =>
        !isSettingsGroup(entry.id) ||
        available.some((child) => settingsGroupOf(child.id) === entry.id)
    )
    .map((entry) => ({
      id: entry.id,
      icon: entry.icon && menuIcons[entry.icon],
      component: pageOf(entry.id) ?? null,
      hidden: entry.hidden
    }));

  useEffect(() => {
    if (!hasWifi && currentTab === 'network-wifi') {
      setCurrentTab('network-general');
    }
  }, [currentTab, hasWifi]);

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

  // As a page the open tab is part of the address: follow the address, and
  // put a tab chosen here (or one a page fell back to) into it.
  const pageTab = page?.tab;
  const onPageTab = page?.onTab;
  useEffect(() => {
    // no page in the address: the host shows something else
    if (!onPageTab || pageTab === undefined) return;
    const tab = pageTabOf(pageTab);
    const group = settingsGroupOf(tab);
    if (group) setExpandedGroups((groups) => new Set(groups).add(group));
    setCurrentTab(tab);
  }, [pageTab, onPageTab]);
  // The address names a page that is not among the entries. The Bluetooth
  // page comes late, so an administrator's address is judged once the device
  // has answered about it; the other accounts have nothing to wait for.
  const pageKnown = !isAdmin || bluetoothKnown;
  const pageMissing =
    pageTab !== undefined &&
    currentTab === pageTabOf(pageTab) &&
    !tabs.some((tab) => tab.id === currentTab);
  const onPageUnavailable = page?.onUnavailable;
  useEffect(() => {
    if (pageMissing && pageKnown) onPageUnavailable?.();
  }, [pageMissing, pageKnown, onPageUnavailable]);
  const onPageLock = page?.onLock;
  useEffect(() => {
    onPageLock?.(isLocked);
    return () => onPageLock?.(false);
  }, [isLocked, onPageLock]);
  const shownTab = useRef(currentTab);
  useEffect(() => {
    if (shownTab.current === currentTab) return;
    shownTab.current = currentTab;
    if (pageTab !== undefined && currentTab !== pageTab) onPageTab?.(currentTab);
  }, [currentTab, pageTab, onPageTab]);

  // After a reload, open the page that was open before it.
  useEffect(() => {
    if (page || !reopenTab) return;
    setRequest(reopenTab);
    reopenTab = null;
  }, [page, setRequest]);

  // Remember the open page for a reload.
  useEffect(() => {
    if (isModalOpen) writeOpenTab(currentTab);
  }, [isModalOpen, currentTab]);

  useEffect(() => {
    // as a page: the shell around it takes the requests
    if (page || !request || isLocked) return;
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
  }, [page, request, isLocked, isModalOpen, setRequest, setKeyboardLock, setSubmenuOpenCount]);

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

    if (isSettingsGroup(tab)) {
      toggleGroup(tab);
      return;
    }
    // An unknown or no longer available tab falls back to the dashboard
    // instead of rendering an empty page.
    const resolved = resolveSettingsTab(tab);
    const target = tabs.some((item) => item.id === resolved) ? resolved : 'dashboard';
    // As a page the address says what is open, and the dashboard is the
    // overview itself.
    if (page) {
      if (target === 'dashboard') page.onClose();
      else page.onTab(target);
      return;
    }
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
    if (page) {
      page.onClose();
      return;
    }

    if (!modalOpenRef.current) return;
    modalOpenRef.current = false;
    writeOpenTab(null);
    setKeyboardLock({ source: 'settings-modal', locked: false });
    setIsModalOpen(false);
    setCurrentTab('dashboard');
    setExpandedGroups(new Set());
    setSubmenuOpenCount((count) => Math.max(0, count - 1));
  }

  // The chat is part of the remote screen.
  function openChat() {
    if (page) {
      setPicoclawOpen(true);
      page.onOpenScreen();
      return;
    }
    closeModal();
    setPicoclawOpen(true);
  }

  function tabTitle(id: string) {
    if (page && id === 'dashboard') return t('overview.title');
    const title = menuTitles.get(id);
    if (!title) return t(`settings.${id}.title`);
    return 'key' in title ? t(title.key) : title.text;
  }

  // "Group / Page" for pages inside a group or a collection.
  function pageTitle(id: string) {
    const parent = settingsParentOf(id);
    return parent ? `${tabTitle(parent)} / ${tabTitle(id)}` : tabTitle(id);
  }

  // A collection item goes back to its list; other pages to the menu (mobile).
  const collection = settingsCollectionOf(currentTab);

  // The open page. heading: its title above it, where nothing around it
  // shows one.
  const openPage = (heading: boolean) => (
    <ErrorBoundary
      key={currentTab}
      fallbackRender={({ resetErrorBoundary }) => (
        <Alert
          type="error"
          showIcon
          message={t('error.title')}
          description={
            <div className="space-y-3">
              <p>{t('error.settingsRecovery')}</p>
              <div className="flex flex-wrap gap-2">
                <Button
                  onClick={() => {
                    retryPages.forEach((retry) => retry());
                    resetErrorBoundary();
                  }}
                >
                  {t('error.retry')}
                </Button>
                <Button onClick={() => window.location.reload()}>{t('error.refresh')}</Button>
              </div>
            </div>
          }
        />
      )}
    >
      {heading && (
        <>
          <div className="flex items-center gap-2">
            {collection && (
              <Button
                type="text"
                size="small"
                aria-label={t('settings.back')}
                disabled={isLocked}
                icon={<ArrowLeftIcon size={16} />}
                onClick={() => changeTab(collection)}
              />
            )}
            <h2 className="text-fg m-0 text-base font-medium">{pageTitle(currentTab)}</h2>
          </div>
          <Divider className="opacity-50" />
        </>
      )}
      <Suspense fallback={<PageLoading />}>
        {/* As a page the address says what is open: a page that is not there
            is not replaced by another under its name. */}
        {tabs.find((tab) => tab.id === currentTab)?.component ??
          (page ? <PageLoading /> : tabs[0].component)}
      </Suspense>
    </ErrorBoundary>
  );

  if (page)
    return page.render({
      entries: tabs.map((tab) => ({
        id: tab.id,
        title: tabTitle(tab.id),
        icon: tab.icon,
        group: settingsGroupOf(tab.id),
        isGroup: isSettingsGroup(tab.id),
        hidden: !!tab.hidden,
        marked: updatesAvailable && (tab.id === 'system' || tab.id === 'system-updates')
      })),
      current: currentTab,
      collection,
      locked: isLocked,
      expanded: expandedGroups,
      open: changeTab,
      title: tabTitle,
      pageTitle,
      content: openPage(false)
    });

  const body = (
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
              onClick={() => (collection ? changeTab(collection) : setDetailOpen(false))}
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
            : 'flex h-full max-w-[260px] min-w-0 shrink-0 flex-col space-y-0.5 overflow-x-hidden overflow-y-auto bg-neutral-800/90 px-1 sm:w-1/5 md:w-1/4 md:px-2 [&>*]:shrink-0',
          !mobile && 'rounded-l-lg'
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
              !tab.hidden &&
              (() => {
                const group = settingsGroupOf(tab.id);
                return !group || expandedGroups.has(group);
              })()
          )
          .map((tab) => {
            const child = settingsGroupOf(tab.id) !== undefined;
            const expanded = isSettingsGroup(tab.id) ? expandedGroups.has(tab.id) : undefined;
            const label = tabTitle(tab.id);
            return (
              <Fragment key={tab.id}>
                <button
                  type="button"
                  disabled={isLocked}
                  aria-label={label}
                  aria-current={currentTab === tab.id || collection === tab.id ? 'page' : undefined}
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
                  <div className="relative flex h-[18px] w-[18px] shrink-0 items-center justify-center">
                    {tab.icon}
                    {updatesAvailable && (tab.id === 'system' || tab.id === 'system-updates') && (
                      <UpdateDot />
                    )}
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
        <div className={clsx('pt-6', !mobile && 'mt-auto!')}>
          <SessionActions isAdmin={isAdmin} disabled={isLocked} mobile={mobile} />
        </div>
        <div className="px-3 pt-2 pb-4">
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
        <div
          className={clsx(
            'box-border flex min-h-0 min-w-0 flex-1 flex-col rounded-r-lg bg-neutral-900/50',
            !mobile && 'pt-14'
          )}
        >
          {/* Keep the desktop close button above, outside the scrolling viewport. */}
          <ScrollArea
            viewportRef={scrollViewportRef}
            className="min-h-0 flex-1 px-3"
            viewportClassName="pr-3"
          >
            <div className="flex min-h-full w-full min-w-0 justify-center">
              <div
                className={clsx(
                  'w-full min-w-0 pb-10',
                  currentTab === 'dashboard' ? 'max-w-[820px]' : 'max-w-[600px]',
                  mobile && 'pt-5'
                )}
              >
                {openPage(!mobile && currentTab !== 'about')}
              </div>
            </div>
          </ScrollArea>
        </div>
      )}
    </div>
  );

  return (
    <>
      <MenuTooltip title={t('settings.title')} placement={tooltipPlacement}>
        <div
          role="button"
          aria-label={
            updatesAvailable
              ? `${t('settings.title')}: ${t('settings.updates.notice.title')}`
              : t('settings.title')
          }
          tabIndex={0}
          onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') {
              event.preventDefault();
              openModal();
            }
          }}
          className="flex h-[30px] w-[30px] cursor-pointer items-center justify-center rounded hover:bg-neutral-700/80"
          onClick={(event) => {
            releasePointerMenuFocus(event);
            openModal();
          }}
        >
          <div className="hover:text-fg-strong relative flex items-center justify-center text-neutral-300">
            <SettingsIcon size={18} className="block" />
            {updatesAvailable && <UpdateDot />}
          </div>
        </div>
      </MenuTooltip>

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
        {body}
      </Modal>
    </>
  );
};
