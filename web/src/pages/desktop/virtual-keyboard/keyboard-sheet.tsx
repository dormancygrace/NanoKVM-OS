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

  useLayoutEffect(() => {
    if (!open) return;
    const previous = document.activeElement;
    const sheet = panel.current;
    return () => {
      drag.current = null;
      setOffset(0);
      // Restore focus only if it is still ours, never after the user has
      // already focused the video or another control behind this sheet.
      if (previous instanceof HTMLElement && sheet?.contains(document.activeElement)) {
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
          if (distance >= 64) onClose();
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
