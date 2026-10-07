import { Fragment, lazy, Suspense, useEffect, useRef } from 'react';
import { Alert, Button, Modal, Spin } from 'antd';
import clsx from 'clsx';
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
  PuzzleIcon,
  SettingsIcon,
  ShieldIcon,
  SmartphoneIcon,
  StethoscopeIcon,
  UsbIcon,
  UserRoundIcon,
  VideoIcon,
  WifiIcon
} from 'lucide-react';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import type { AddonInventory } from '@/api/addons';
import { Netbird as NetbirdIcon } from '@/components/icons/netbird';
import { OpenVPNIcon } from '@/components/icons/openvpn';
import { RustDeskIcon } from '@/components/icons/rustdesk';
import { Tailscale as TailscaleIcon } from '@/components/icons/tailscale';
import { WireGuardIcon } from '@/components/icons/wireguard';
import { ScrollArea } from '@/components/ui/scroll-area';

import styles from './sidebar.module.css';

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
const Device = lazy(() => import('./device').then((module) => ({ default: module.Device })));
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

export type SettingsDialogProps = {
  isModalOpen: boolean;
  mobile: boolean;
  isAdmin: boolean;
  isLocked: boolean;
  setIsLocked: (locked: boolean) => void;
  currentTab: string;
  detailOpen: boolean;
  setDetailOpen: (open: boolean) => void;
  vpnExpanded: boolean;
  networkExpanded: boolean;
  systemExpanded: boolean;
  softwareExpanded: boolean;
  extensionsExpanded: boolean;
  inventory: AddonInventory | null;
  inventoryError: boolean;
  changeTab: (tab: string) => void;
  closeModal: () => void;
  openPicoclaw: () => void;
};

// The settings modal body, loaded the first time the modal is opened.
export const SettingsDialog = ({
  isModalOpen,
  mobile,
  isAdmin,
  isLocked,
  setIsLocked,
  currentTab,
  detailOpen,
  setDetailOpen,
  vpnExpanded,
  networkExpanded,
  systemExpanded,
  softwareExpanded,
  extensionsExpanded,
  inventory,
  inventoryError,
  changeTab,
  closeModal,
  openPicoclaw
}: SettingsDialogProps) => {
  const { t } = useTranslation();
  const { i18n } = useTranslation();
  const extensionsTitle = (i18n.resolvedLanguage || i18n.language).startsWith('ru')
    ? 'Расширения'
    : 'Extensions';
  const scrollViewportRef = useRef<HTMLDivElement>(null);

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
          {
            id: 'system-diagnostics',
            icon: <StethoscopeIcon size={16} />,
            component: <Diagnostics />
          },
          { id: 'system-memory', icon: <MemoryStickIcon size={16} />, component: <Memory /> },
          {
            id: 'system-date-time',
            icon: <ClockIcon size={16} />,
            component: <DateTimeSettings />
          },
          { id: 'system-users', icon: <UserRoundIcon size={16} />, component: <Account /> },
          { id: 'system-mcp', icon: <BotIcon size={16} />, component: <MCP /> },
          { id: 'system-updates', icon: <DownloadIcon size={16} />, component: <Updates /> },
          { id: 'software', icon: <PackageIcon size={16} />, component: null },
          {
            id: 'software-addons',
            icon: <BotIcon size={16} />,
            component: (
              <Addons
                onOpenRustDesk={() => changeTab('extensions-rustdesk')}
                onOpen={openPicoclaw}
              />
            )
          },
          { id: 'software-packages', icon: <PackageIcon size={16} />, component: <Software /> },
          { id: 'extensions', icon: <PuzzleIcon size={16} />, component: null },
          ...(inventory?.rustdesk.installed
            ? [
                {
                  id: 'extensions-rustdesk',
                  icon: <RustDeskIcon size={18} />,
                  component: <RustDeskControls />
                }
              ]
            : []),
          ...(inventory?.picoclaw.installed
            ? [
                {
                  id: 'extensions-picoclaw',
                  icon: <BotIcon size={16} />,
                  component: null
                }
              ]
            : []),
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
          }
        ]
      : []),
    { id: 'appearance', icon: <PaletteIcon size={16} />, component: <Appearance /> },
    ...(!isAdmin
      ? [{ id: 'account', icon: <UserRoundIcon size={18} />, component: <Account /> }]
      : []),
    { id: 'about', icon: <InfoIcon size={14} />, component: <About /> }
  ];

  useEffect(() => {
    scrollViewportRef.current?.scrollTo({ top: 0, left: 0 });
  }, [currentTab]);

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
    if (id === 'system-diagnostics') return t('settings.system.diagnostics.title');
    if (id === 'system-memory') return t('settings.memory.title');
    if (id === 'system-users') return t('settings.account.title');
    if (id === 'system-mcp') return t('settings.mcp.title');
    if (id === 'system-updates') return t('settings.updates.title');
    if (id === 'extensions') return extensionsTitle;
    if (id === 'extensions-rustdesk') return 'RustDesk';
    if (id === 'extensions-picoclaw') return 'PicoClaw';
    if (id === 'software') return t('settings.software.title');
    if (id === 'software-addons') return t('settings.software.addons.title');
    if (id === 'software-packages') return t('settings.software.addons.packages');
    if (id === 'account') return t('settings.account.title');
    if (id === 'vpn') return 'VPN';
    if (id === 'vpn-tailscale') return 'Tailscale';
    if (id === 'vpn-netbird') return 'NetBird';
    if (id === 'vpn-wireguard') return 'WireGuard';
    if (id === 'vpn-openvpn') return 'OpenVPN';
    return t(`settings.${id}.title`);
  }

  return (
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
                (!tab.id.startsWith('vpn-') || vpnExpanded) &&
                (!tab.id.startsWith('network-') || networkExpanded) &&
                (!tab.id.startsWith('system-') || systemExpanded) &&
                (!tab.id.startsWith('software-') || softwareExpanded) &&
                (!tab.id.startsWith('extensions-') || extensionsExpanded)
            )
            .map((tab) => {
              const child =
                tab.id.startsWith('vpn-') ||
                tab.id.startsWith('network-') ||
                tab.id.startsWith('system-') ||
                tab.id.startsWith('software-') ||
                tab.id.startsWith('extensions-');
              const expanded =
                tab.id === 'vpn'
                  ? vpnExpanded
                  : tab.id === 'network'
                    ? networkExpanded
                    : tab.id === 'system'
                      ? systemExpanded
                      : tab.id === 'software'
                        ? softwareExpanded
                        : tab.id === 'extensions'
                          ? extensionsExpanded
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
                  {tab.id === 'extensions' &&
                    extensionsExpanded &&
                    !inventory?.rustdesk.installed &&
                    !inventory?.picoclaw.installed && (
                      <div className="ml-4 px-3 py-2 text-xs text-neutral-400" role="status">
                        {!inventory && !inventoryError ? (
                          <Spin size="small" />
                        ) : inventoryError ? (
                          (i18n.resolvedLanguage || i18n.language).startsWith('ru') ? (
                            'Не удалось загрузить расширения'
                          ) : (
                            'Could not load extensions'
                          )
                        ) : (i18n.resolvedLanguage || i18n.language).startsWith('ru') ? (
                          'Нет установленных расширений'
                        ) : (
                          'No extensions installed'
                        )}
                      </div>
                    )}
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
                <ErrorBoundary
                  key={currentTab}
                  fallback={<Alert type="error" showIcon message={t('error.title')} />}
                >
                  <Suspense fallback={<PageLoading />}>
                    {tabs.find((tab) => tab.id === currentTab)?.component}
                  </Suspense>
                </ErrorBoundary>
              </div>
            </div>
          </ScrollArea>
        )}
      </div>
    </Modal>
  );
};
