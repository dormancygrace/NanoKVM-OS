import {
  cloneElement,
  useContext,
  useEffect,
  useRef,
  useState,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
  type TouchEvent
} from 'react';
import { Tooltip, type TooltipProps } from 'antd';

import { useMediaQuery } from '@/hooks/useMediaQuery.ts';

import { MobileMenuItemContext } from './mobile-menu-context.ts';

// How long a finger rests on a button before its label shows, and how long
// the label stays after the finger is gone.
const LONG_PRESS_MS = 450;
const LINGER_MS = 1200;

type TriggerProps = {
  onTouchStart?: (event: TouchEvent) => void;
  onTouchMove?: (event: TouchEvent) => void;
  onTouchEnd?: (event: TouchEvent) => void;
  onTouchCancel?: (event: TouchEvent) => void;
  onClickCapture?: (event: MouseEvent) => void;
  onContextMenu?: (event: MouseEvent) => void;
};

type MenuTooltipProps = {
  title: ReactNode;
  // Beside the mobile rail by default (the side away from the screen's edge),
  // below the desktop toolbar.
  placement?: TooltipProps['placement'];
  // For a trigger that also opens a popover or a dialog: the caller's own
  // hover state, used where there is a pointer that hovers.
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  // Nothing is shown while this is set (the trigger's popover is open).
  blocked?: boolean;
  overlayClassName?: string;
  children: ReactElement<TriggerProps>;
};

// The label of a button of the menu bar, the same for every button: one look
// (the mobile rail's popover class when inside the rail) and one behaviour.
// With a mouse it shows on hover and focus. A touch screen has no hover: a tap
// used to leave the label on the screen until something else was tapped. There
// a long press shows it, a tap does not, and it goes away by itself.
export function MenuTooltip({
  title,
  placement,
  open,
  onOpenChange,
  blocked = false,
  overlayClassName,
  children
}: MenuTooltipProps) {
  const context = useContext(MobileMenuItemContext);
  const touch = useMediaQuery('(hover: none)');
  const [pressed, setPressed] = useState(false);
  const pressTimer = useRef<number | undefined>(undefined);
  const lingerTimer = useRef<number | undefined>(undefined);
  const swallowClick = useRef(false);

  useEffect(
    () => () => {
      window.clearTimeout(pressTimer.current);
      window.clearTimeout(lingerTimer.current);
    },
    []
  );

  const side =
    placement ??
    (context.mobilePlacement ? (context.mobilePlacement === 'right' ? 'left' : 'right') : 'bottom');
  const classNames = { root: overlayClassName ?? context.mobilePopoverClassName };

  if (!touch) {
    return (
      <Tooltip
        title={title}
        trigger={['hover', 'focus']}
        mouseEnterDelay={0.6}
        placement={side}
        classNames={classNames}
        open={blocked ? false : open}
        onOpenChange={onOpenChange}
      >
        {children}
      </Tooltip>
    );
  }

  const cancel = () => {
    window.clearTimeout(pressTimer.current);
    window.clearTimeout(lingerTimer.current);
  };
  const child = children.props;
  const trigger = cloneElement(children, {
    onTouchStart: (event: TouchEvent) => {
      child.onTouchStart?.(event);
      cancel();
      swallowClick.current = false;
      pressTimer.current = window.setTimeout(() => {
        swallowClick.current = true;
        setPressed(true);
      }, LONG_PRESS_MS);
    },
    // The finger moved: the rail is being scrolled or dragged.
    onTouchMove: (event: TouchEvent) => {
      child.onTouchMove?.(event);
      cancel();
      setPressed(false);
    },
    onTouchEnd: (event: TouchEvent) => {
      child.onTouchEnd?.(event);
      cancel();
      lingerTimer.current = window.setTimeout(() => setPressed(false), LINGER_MS);
    },
    onTouchCancel: (event: TouchEvent) => {
      child.onTouchCancel?.(event);
      cancel();
      setPressed(false);
    },
    // The press that asked for the label is not also a press of the button.
    onClickCapture: (event: MouseEvent) => {
      if (swallowClick.current) {
        swallowClick.current = false;
        event.preventDefault();
        event.stopPropagation();
        return;
      }
      child.onClickCapture?.(event);
    },
    onContextMenu: (event: MouseEvent) => {
      event.preventDefault();
      child.onContextMenu?.(event);
    }
  });

  return (
    <Tooltip
      title={title}
      trigger={[]}
      placement={side}
      classNames={classNames}
      open={pressed && !blocked}
    >
      {trigger}
    </Tooltip>
  );
}
