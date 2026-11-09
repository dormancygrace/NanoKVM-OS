import * as React from 'react';
import clsx from 'clsx';

/*
 * A vertically scrolling area with the native scrollbar, styled like every
 * other one by index.css (6 px, rounded, no buttons). The browser draws and
 * drives it: wheel, touch, keyboard and dragging work without script. viewportClassName styles the scrolling box
 * itself (for example its maximum height); viewportRef reaches it to scroll
 * from code.
 */
function ScrollArea({
  className,
  viewportClassName,
  viewportRef,
  children,
  ...props
}: React.ComponentProps<'div'> & {
  viewportClassName?: string;
  viewportRef?: React.Ref<HTMLDivElement>;
}) {
  return (
    <div data-slot="scroll-area" className={clsx('relative', className)} {...props}>
      <div
        ref={viewportRef}
        data-slot="scroll-area-viewport"
        className={clsx(
          'focus-visible:ring-ring/50 size-full min-w-0 overflow-x-hidden overflow-y-auto rounded-[inherit] transition-[color,box-shadow] outline-none focus-visible:ring-[3px] focus-visible:outline-1',
          viewportClassName
        )}
      >
        {children}
      </div>
    </div>
  );
}

export { ScrollArea };
