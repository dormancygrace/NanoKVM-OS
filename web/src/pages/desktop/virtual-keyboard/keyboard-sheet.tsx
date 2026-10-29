import { useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';

// A non-modal keyboard must leave the video and toolbar interactive and must
// not take focus away from them when it opens. Only its handle is draggable:
// gestures on keys and language controls belong to those controls.
export function KeyboardSheet({
  open,
  label,
  onClose,
  className,
  children
}: {
  open: boolean;
  label: string;
  onClose: () => void;
  className: string;
  children: ReactNode;
}) {
  const panel = useRef<HTMLElement>(null);
  const drag = useRef<{ id: number; y: number } | null>(null);
  const [offset, setOffset] = useState(0);
  const closeFrame = useRef<number | null>(null);

  useLayoutEffect(() => {
    if (!open) return;
    const previous = document.activeElement;
    const sheet = panel.current;
    let focusedInside = sheet?.contains(document.activeElement) ?? false;
    const trackFocus = (event: FocusEvent) => {
      focusedInside = sheet?.contains(event.target as Node) ?? false;
    };
    document.addEventListener('focusin', trackFocus);
    return () => {
      document.removeEventListener('focusin', trackFocus);
      drag.current = null;
      if (closeFrame.current !== null) cancelAnimationFrame(closeFrame.current);
      closeFrame.current = null;
      setOffset(0);
      // React can remove the portal before layout cleanup, moving the active
      // element to body without a focusin event. Remember whether focus was
      // ours, but never steal it from a control chosen outside the sheet.
      const active = document.activeElement;
      if (
        previous instanceof HTMLElement &&
        (sheet?.contains(active) || (focusedInside && active === document.body))
      ) {
        previous.focus({ preventScroll: true });
      }
    };
  }, [open]);

  if (!open) return null;

  return createPortal(
    <section
      ref={panel}
      role="dialog"
      aria-modal="false"
      aria-label={label}
      className={className}
      style={{ transform: `translateY(${offset}px)` }}
      onKeyDown={(event) => {
        if (event.key === 'Escape' && !event.defaultPrevented) {
          event.stopPropagation();
          setOffset(0);
          onClose();
        }
      }}
    >
      <div
        aria-hidden="true"
        className="flex h-5 cursor-grab touch-none items-center justify-center"
        onPointerDown={(event) => {
          if (!event.isPrimary || event.button !== 0) return;
          drag.current = { id: event.pointerId, y: event.clientY };
          event.currentTarget.setPointerCapture(event.pointerId);
          event.preventDefault();
        }}
        onPointerMove={(event) => {
          if (drag.current?.id === event.pointerId) {
            setOffset(Math.max(0, event.clientY - drag.current.y));
          }
        }}
        onPointerUp={(event) => {
          if (drag.current?.id !== event.pointerId) return;
          const distance = event.clientY - drag.current.y;
          drag.current = null;
          setOffset(0);
          if (distance >= 64) {
            // Keep the native target alive until touchend has bubbled; it is
            // dispatched after pointerup for the same finger release.
            closeFrame.current = requestAnimationFrame(() => {
              closeFrame.current = null;
              onClose();
            });
          }
        }}
        onLostPointerCapture={() => {
          drag.current = null;
          setOffset(0);
        }}
      >
        <div className="h-1 w-10 rounded-full bg-neutral-400" />
      </div>
      {children}
    </section>,
    document.body
  );
}
