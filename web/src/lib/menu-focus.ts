import type { MouseEvent } from 'react';

// Pointer users return to remote keyboard input after dismissing the menu.
// Do this before a dialog records its trigger for focus restoration. Keyboard
// activation has detail === 0 and must retain focus for accessible navigation.
export function releasePointerMenuFocus(event: MouseEvent<HTMLElement>) {
  if (event.detail > 0) event.currentTarget.blur();
}
