import { RefObject, useCallback, useEffect, useState } from 'react';
import { useAtomValue } from 'jotai';

import { menuDisabledItemsAtom } from '@/jotai/settings.ts';

interface MenuBounds {
  left: number;
  right: number;
  top: number;
  bottom: number;
}

export function useMenuBounds(
  nodeRef: RefObject<HTMLDivElement | null>,
  isMenuExpanded: boolean,
  active: boolean
): MenuBounds {
  const menuDisabledItems = useAtomValue(menuDisabledItemsAtom);
  const [menuBounds, setMenuBounds] = useState<MenuBounds>({
    left: 0,
    right: 0,
    top: 0,
    bottom: 0
  });

  const handleResize = useCallback(() => {
    if (!nodeRef.current) return;

    const elementRect = nodeRef.current.getBoundingClientRect();
    const width = (window.innerWidth - elementRect.width) / 2;

    setMenuBounds({
      left: -width,
      top: -10,
      right: width,
      bottom: window.innerHeight - elementRect.height - 10
    });
  }, [nodeRef]);

  // Update bounds on mount and resize
  useEffect(() => {
    if (!active) return;
    handleResize();
    window.addEventListener('resize', handleResize);
    // Lazy items and the optional LED panels can change the menu width.
    const observer = new ResizeObserver(handleResize);
    if (nodeRef.current) observer.observe(nodeRef.current);

    return () => {
      window.removeEventListener('resize', handleResize);
      observer.disconnect();
    };
  }, [active, handleResize, nodeRef]);

  // Update bounds when menu items or expansion state change
  useEffect(() => {
    handleResize();
  }, [menuDisabledItems, isMenuExpanded, handleResize]);

  return menuBounds;
}
