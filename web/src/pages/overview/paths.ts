import type { MouseEvent } from 'react';

// The address of a settings page.
export const settingsPath = (tab: string) => `/settings/${encodeURIComponent(tab)}`;

// The address of the pages of one settings group, as a list.
export const settingsGroupPath = (group: string) => `/settings?g=${encodeURIComponent(group)}`;

// A link that leads away from the page, switched off while the page is at
// work and must stay (locked).
export const wayOf = (locked: boolean) =>
  locked
    ? {
        props: {
          'aria-disabled': true,
          tabIndex: -1,
          onClick: (event: MouseEvent) => event.preventDefault()
        } as const,
        className: 'pointer-events-none opacity-50'
      }
    : { props: {}, className: '' };
