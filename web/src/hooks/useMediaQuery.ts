import { useCallback, useSyncExternalStore } from 'react';

/*
 * Whether a CSS media query matches, following changes (resize, rotation,
 * zoom): for example useMediaQuery('(min-width: 850px)').
 */
export function useMediaQuery(query: string): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      const list = window.matchMedia(query);
      list.addEventListener('change', onChange);
      return () => list.removeEventListener('change', onChange);
    },
    [query]
  );
  const matches = useCallback(() => window.matchMedia(query).matches, [query]);
  return useSyncExternalStore(subscribe, matches, () => false);
}
