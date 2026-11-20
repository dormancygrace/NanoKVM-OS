import { lazy, Suspense, useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { useAuth } from '@/contexts/auth.ts';
import { Button, Divider } from 'antd';
import clsx from 'clsx';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';
import { ChevronDownIcon, GripVerticalIcon } from 'lucide-react';
import Draggable, { DraggableData, DraggableEvent } from 'react-draggable';
import { useTranslation } from 'react-i18next';

import { getRuntimeStatus } from '@/api/picoclaw.ts';
import { subscribeLiveStatus } from '@/lib/live-status.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { atxEnabledAtom, atxLocalChange } from '@/jotai/atx.ts';
import { isKeyboardOpenAtom } from '@/jotai/keyboard.ts';
import { picoclawChatOpenAtom, picoclawRuntimeStatusAtom } from '@/jotai/picoclaw.ts';
import { isHdmiEnabledAtom } from '@/jotai/screen.ts';
import {
  hostLedStatusVisibleAtom,
  keyboardLedStatusVisibleAtom,
  menuCloseSignalAtom,
  menuDisabledItemsAtom
} from '@/jotai/settings.ts';
import { useMenuBounds } from '@/hooks/useMenuBounds.ts';
import { useMenuVisibility } from '@/hooks/useMenuVisibility.ts';
import { useResponsiveDevice } from '@/hooks/useResponsiveDevice.ts';
import { useUsbAudio } from '@/hooks/useUsbAudio.ts';
import { useUsbInput } from '@/hooks/useUsbInput.ts';
import { MobileMenuItemProvider } from '@/components/menu-item.tsx';
import { ThemeToggle } from '@/components/theme-toggle.tsx';

import { Control } from '../control.tsx';
import { HostLedStatus } from '../host-led-status';
import { KeyboardLedStatus } from '../keyboard-led-status';
import { AudioMenu } from './audio';
import { Fullscreen } from './fullscreen';
import { Keyboard } from './keyboard';
import { CtrlAltDel } from './keyboard/ctrl-alt-del.tsx';
import { Mouse } from './mouse';
import { Collapse, Expand } from './operations';
import { OverviewLink } from './overview.tsx';
import { Power } from './power';
import { Recorder } from './recorder';
import { Screen } from './screen';
import { Screenshot } from './screenshot';
import { Settings } from './settings';
import { Wol } from './wol';

// Admin-only items come from one lazily loaded chunk (see admin-items.ts).
const loadAdminItems = () => import('./admin-items.ts');
const Image = lazy(() => loadAdminItems().then((module) => ({ default: module.Image })));
const Picoclaw = lazy(() => loadAdminItems().then((module) => ({ default: module.Picoclaw })));
const Script = lazy(() => loadAdminItems().then((module) => ({ default: module.Script })));
const ScreenText = lazy(() => loadAdminItems().then((module) => ({ default: module.ScreenText })));
const Terminal = lazy(() => loadAdminItems().then((module) => ({ default: module.Terminal })));
const UsbMenu = lazy(() => loadAdminItems().then((module) => ({ default: module.UsbMenu })));

// Keeps a lazy toolbar item's slot while its chunk loads, so the bar
// does not shift. Image renders nothing until the virtual disk state is known,
// so its placeholder is empty as well.
const ToolbarSlot = () => <div className="size-9" aria-hidden="true" />;
function lazyItem(key: string, item: ReactNode, placeholder: ReactNode = <ToolbarSlot />) {
  return (
    <Suspense key={key} fallback={placeholder}>
      {item}
    </Suspense>
  );
}

const mobileMenuPopoverClassName = 'nanokvm-mobile-menu-popover';

// desktop: the floating bar; bottom and side: a phone's bar, held upright or
// turned sideways.
type MenuVariant = 'desktop' | 'bottom' | 'side';

export const Menu = () => {
  const audio = useUsbAudio();
  const usbInput = useUsbInput();
  const { t } = useTranslation();
  const nodeRef = useRef<HTMLDivElement | null>(null);
  const { account } = useAuth();
  const isAdmin = account.role === 'admin';
  const picoclawStatus = useAtomValue(picoclawRuntimeStatusAtom);
  const setPicoclawStatus = useSetAtom(picoclawRuntimeStatusAtom);
  const setPicoclawOpen = useSetAtom(picoclawChatOpenAtom);
  useEffect(() => {
    if (!isAdmin) return;
    let active = true;
    let pending = false;
    const refresh = async () => {
      if (pending) return;
      pending = true;
      try {
        const response = await getRuntimeStatus();
        if (active && response.code === 0) setPicoclawStatus(response.data);
      } catch {
        /* Keep the last known state through brief disconnects. */
      } finally {
        pending = false;
      }
    };
    void refresh();
    const stop = pollWhileVisible(() => void refresh(), 30000);
    return () => {
      active = false;
      stop();
    };
  }, [isAdmin, setPicoclawStatus]);
  useEffect(() => {
    if (picoclawStatus?.installed === false) setPicoclawOpen(false);
  }, [picoclawStatus?.installed, setPicoclawOpen]);
  const captureEnabled = useAtomValue(isHdmiEnabledAtom);
  const isKeyboardOpen = useAtomValue(isKeyboardOpenAtom);

  const menuDisabledItems = useAtomValue(menuDisabledItemsAtom);
  // The power button needs ATX power control on (System > Device). The shared
  // live status follows a change made in another tab or by another admin, and
  // a request that failed is simply answered by the next one.
  const [atxEnabled, setAtxEnabled] = useAtom(atxEnabledAtom);
  useEffect(
    () =>
      subscribeLiveStatus((status, startedAt) => {
        if (typeof status?.atx === 'boolean' && startedAt >= atxLocalChange.at) {
          setAtxEnabled(status.atx);
        }
      }),
    [setAtxEnabled]
  );
  const menuCloseSignal = useAtomValue(menuCloseSignalAtom);
  const isKeyboardLedStatusVisible = useAtomValue(keyboardLedStatusVisibleAtom);
  const isHostLedStatusVisible = useAtomValue(hostLedStatusVisibleAtom);
  const [dismissMenuKey, setDismissMenuKey] = useState(0);

  const {
    isInitialized,
    isMenuExpanded,
    isMenuHidden,
    handleHovered,
    handleMoved,
    setIsMenuExpanded
  } = useMenuVisibility();

  const responsiveDevice = useResponsiveDevice();
  const phoneBar: MenuVariant | null = responsiveDevice.isMobilePortrait
    ? 'bottom'
    : responsiveDevice.isPhoneLandscape
      ? 'side'
      : null;
  const isMobileRailActive = phoneBar !== null;
  const menuBounds = useMenuBounds(nodeRef, isMenuExpanded, !isMobileRailActive);
  const isTouchLandscapeMenu =
    responsiveDevice.isTouchLikeDevice && responsiveDevice.isLandscapeViewport;
  const showKeyboardLedStatus = usbInput.keyboard && isKeyboardLedStatusVisible;
  // The host's LEDs come from the ATX header, as the power button does.
  const showHostLedStatus = atxEnabled && isHostLedStatusVisible;
  // The two panels sit on opposite sides of the menu, at the ends of the
  // phone's bottom bar too.

  useEffect(() => {
    if (menuCloseSignal > 0) {
      setIsMenuExpanded(false);
    }
  }, [menuCloseSignal, setIsMenuExpanded]);

  function onDragStop(_e: DraggableEvent, data: DraggableData) {
    if (data.x === 0 && data.y === 0) return;
    handleMoved();
  }

  const dismissMenus = useCallback(() => {
    setDismissMenuKey((key) => key + 1);
  }, []);

  useEffect(() => {
    if (!isMenuExpanded || isMenuHidden) dismissMenus();
  }, [isMenuExpanded, isMenuHidden, dismissMenus]);

  function onMenuDragStart() {
    dismissMenus();
  }

  function isEnabled(item: string) {
    return !menuDisabledItems.includes(item);
  }

  function renderDivider(variant: MenuVariant, key: string) {
    if (variant === 'bottom') {
      return <div key={key} className="mx-1 h-6 w-px shrink-0 bg-neutral-600/60" />;
    }
    if (variant === 'side') {
      return <div key={key} className="my-1 h-px w-6 shrink-0 bg-neutral-600/60" />;
    }

    return <Divider key={key} type="vertical" />;
  }

  function renderMenuItems(variant: MenuVariant): ReactNode[] {
    const items: ReactNode[] = [];
    const tooltipPlacement = variant === 'bottom' ? 'top' : variant === 'side' ? 'left' : 'bottom';

    // Only join non-empty groups, including when appearance settings hide tools.
    const groups: ReactNode[][] = [
      [
        <Screen key="screen" />,
        <Screenshot key="screenshot" />,
        ...(isAdmin ? [lazyItem('screen-text', <ScreenText />, null)] : []),
        ...(isEnabled('recorder') ? [<Recorder key="recorder" />] : [])
      ],
      audio.available ? [<AudioMenu key="audio" audio={audio} />] : [],
      [
        ...(usbInput.available ? [<Control key="control" />] : []),
        ...(captureEnabled && usbInput.keyboard && isEnabled('keyboard')
          ? [<Keyboard key="keyboard" quickAccess={responsiveDevice.isTouchLikeDevice} />]
          : []),
        ...(captureEnabled && usbInput.keyboard && isEnabled('ctrl-alt-del')
          ? [<CtrlAltDel key="ctrl-alt-del" />]
          : []),
        ...(captureEnabled && (usbInput.absolute || usbInput.relative) && isEnabled('mouse')
          ? [<Mouse key="mouse" />]
          : [])
      ],
      isAdmin
        ? [
            lazyItem('usb', <UsbMenu />),
            ...(isEnabled('image')
              ? [lazyItem('image', <Image tooltipPlacement={tooltipPlacement} />, null)]
              : [])
          ]
        : [],
      isAdmin
        ? [
            ...(isEnabled('terminal') ? [lazyItem('terminal', <Terminal />)] : []),
            ...(isEnabled('script') ? [lazyItem('script', <Script />)] : []),
            ...(picoclawStatus?.installed === true && isEnabled('picoclaw')
              ? [lazyItem('picoclaw', <Picoclaw tooltipPlacement={tooltipPlacement} />)]
              : [])
          ]
        : [],
      [
        ...(isEnabled('wol') ? [<Wol key="wol" />] : []),
        ...(atxEnabled && isEnabled('power')
          ? [<Power key="power" vertical={variant === 'side'} />]
          : [])
      ],
      variant !== 'desktop'
        ? [
            ...(isEnabled('theme') ? [<ThemeToggle key="theme" bar />] : []),
            <Settings key="settings" tooltipPlacement={tooltipPlacement} />
          ]
        : []
    ];

    groups
      .filter((group) => group.length > 0)
      .forEach((group, index) => {
        if (index > 0) items.push(renderDivider(variant, `divider-group-${index}`));
        items.push(...group);
      });
    // Pointer preferences must still initialize when its toolbar button is hidden.
    if (captureEnabled && (!isEnabled('mouse') || !(usbInput.absolute || usbInput.relative)))
      items.push(<Mouse key="mouse" hidden />);
    return items;
  }

  if (phoneBar) {
    // A phone: the bar along the bottom edge when held upright, along the
    // right edge when turned sideways, where the picture is limited by the
    // height and loses nothing to it. The console page leaves the bar its room.
    // Its menus open towards the screen.
    const side = phoneBar === 'side';
    return (
      <div
        className={clsx(
          'fixed transition-opacity duration-300',
          side ? 'inset-y-0 right-0' : 'inset-x-0 bottom-0'
        )}
        style={{
          zIndex: isKeyboardOpen ? 998 : 1000,
          opacity: isInitialized ? 1 : 0,
          ...(side
            ? { paddingRight: 'env(safe-area-inset-right)' }
            : { paddingBottom: 'env(safe-area-inset-bottom)' })
        }}
      >
        <MobileMenuItemProvider
          mobilePlacement={side ? 'right' : 'bottom'}
          mobilePopoverClassName={mobileMenuPopoverClassName}
          dismissMenuKey={dismissMenuKey}
          onRequestMobileMenuDismiss={dismissMenus}
        >
          <div
            className={clsx(
              'nanokvm-touch-toolbar nanokvm-phone-bar border-line box-border flex gap-1 bg-neutral-900',
              side
                ? 'h-full w-14 flex-col items-center border-l py-1.5'
                : 'h-14 items-center border-t px-1.5'
            )}
          >
            {showKeyboardLedStatus && (
              <div className={clsx('flex shrink-0', side ? 'h-12' : 'h-9')}>
                <KeyboardLedStatus />
              </div>
            )}
            <OverviewLink />
            {/* The items scroll when they do not fit; all of them stay mounted:
                a browser ISO, a recording or an open dialog belongs to its
                item. */}
            <div
              className={clsx(
                'flex flex-1 [scrollbar-width:none] items-center overscroll-contain *:shrink-0 [&::-webkit-scrollbar]:hidden',
                side
                  ? 'min-h-0 flex-col overflow-x-hidden overflow-y-auto'
                  : 'min-w-0 overflow-x-auto overflow-y-hidden'
              )}
            >
              {renderMenuItems(phoneBar)}
            </div>
            {showHostLedStatus && (
              <div className={clsx('flex shrink-0', side ? 'h-12' : 'h-9')}>
                <HostLedStatus />
              </div>
            )}
          </div>
        </MobileMenuItemProvider>
      </div>
    );
  }

  return (
    <Draggable
      nodeRef={nodeRef}
      bounds={menuBounds}
      handle="strong"
      onStart={onMenuDragStart}
      onStop={onDragStop}
    >
      <div
        ref={nodeRef}
        className={clsx(
          'pointer-events-none fixed top-[10px] left-1/2 z-1000 w-max max-w-[calc(100vw-16px)] transition-opacity duration-300',
          isInitialized ? 'opacity-100' : 'opacity-0'
        )}
      >
        <div
          className="relative -translate-x-1/2"
          onMouseEnter={() => handleHovered(true)}
          onMouseLeave={() => handleHovered(false)}
          onFocus={() => handleHovered(true)}
          onBlur={() => handleHovered(false)}
        >
          {isMenuExpanded && (
            <Button
              type="text"
              aria-label={t('menu.expand')}
              className={clsx(
                'absolute z-10 !p-0 transition-all duration-300',
                isTouchLandscapeMenu
                  ? 'top-0 left-1/2 flex! !h-10 !w-16 !min-w-16 -translate-x-1/2 items-center! justify-center! !rounded-b !bg-neutral-800/80 text-neutral-300 shadow-lg shadow-black/30 backdrop-blur'
                  : 'top-[-10px] left-0 h-[54px]! w-full! bg-transparent!',
                isMenuHidden ? 'pointer-events-auto opacity-100' : 'pointer-events-none opacity-0'
              )}
              onPointerDown={(event) => {
                handleHovered(true);
                event.currentTarget.setPointerCapture(event.pointerId);
              }}
              onPointerUp={(event) => {
                handleHovered(false);
                if (event.currentTarget.hasPointerCapture(event.pointerId)) {
                  event.currentTarget.releasePointerCapture(event.pointerId);
                }
              }}
              onPointerCancel={(event) => {
                handleHovered(false);
                if (event.currentTarget.hasPointerCapture(event.pointerId)) {
                  event.currentTarget.releasePointerCapture(event.pointerId);
                }
              }}
            >
              {isTouchLandscapeMenu && <ChevronDownIcon size={16} aria-hidden="true" />}
            </Button>
          )}

          <div className="sticky top-[10px] flex w-full justify-center">
            {isMenuExpanded && showKeyboardLedStatus && !isTouchLandscapeMenu && (
              <div
                className={clsx(
                  'mr-1.5 flex h-11 shrink-0 transition-all duration-300',
                  isMenuHidden ? 'pointer-events-none opacity-0' : 'pointer-events-auto opacity-100'
                )}
              >
                <KeyboardLedStatus />
              </div>
            )}
            <div
              className={clsx(
                'nanokvm-console-bar border-line pointer-events-auto min-w-0 items-center rounded-[14px] border bg-neutral-900/90 pr-1.5 pl-1 shadow-lg shadow-black/30 backdrop-blur transition-all duration-300',
                isTouchLandscapeMenu ? 'nanokvm-touch-toolbar h-[44px]' : 'h-11',
                isMenuExpanded ? 'flex' : 'hidden',
                isMenuHidden ? 'translate-y-[-110%] opacity-80' : 'translate-y-0 opacity-100'
              )}
            >
              <strong className="shrink-0">
                <div className="flex h-9 cursor-move items-center justify-center px-1 text-neutral-500 select-none">
                  <GripVerticalIcon size={18} />
                </div>
              </strong>
              {isMenuExpanded && showKeyboardLedStatus && isTouchLandscapeMenu && (
                <div className="ml-1 flex h-7 shrink-0">
                  <KeyboardLedStatus />
                </div>
              )}
              <OverviewLink />
              <Divider type="vertical" className="shrink-0" />

              <MobileMenuItemProvider dismissMenuKey={dismissMenuKey}>
                <div className="nanokvm-mobile-menu-rail flex min-w-0 items-center overflow-x-auto *:shrink-0">
                  {renderMenuItems('desktop')}
                </div>
                <Divider type="vertical" className="shrink-0" />
                {isEnabled('theme') && <ThemeToggle bar />}
                <Settings />
                {isEnabled('fullscreen') && <Fullscreen />}
                {isEnabled('collapse') && <Collapse toggleMenu={setIsMenuExpanded} />}
              </MobileMenuItemProvider>
              {/* The touch toolbar takes the whole width: the panels are inside it, at its ends. */}
              {isMenuExpanded && showHostLedStatus && isTouchLandscapeMenu && (
                <>
                  <Divider type="vertical" className="shrink-0" />
                  <div className="flex h-7 shrink-0">
                    <HostLedStatus />
                  </div>
                </>
              )}
            </div>
            {isMenuExpanded && showHostLedStatus && !isTouchLandscapeMenu && (
              <div
                className={clsx(
                  'ml-1.5 flex h-11 shrink-0 transition-all duration-300',
                  isMenuHidden ? 'pointer-events-none opacity-0' : 'pointer-events-auto opacity-100'
                )}
              >
                <HostLedStatus />
              </div>
            )}
          </div>

          {!isMenuExpanded && (
            <div
              className={clsx(
                'pointer-events-auto inline-flex w-max',
                isTouchLandscapeMenu && 'nanokvm-touch-toolbar'
              )}
            >
              <Expand toggleMenu={setIsMenuExpanded} />
            </div>
          )}
        </div>
      </div>
    </Draggable>
  );
};
