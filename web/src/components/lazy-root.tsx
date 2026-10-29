import { lazy, Suspense } from 'react';

const Root = lazy(() => import('./root').then(({ Root }) => ({ default: Root })));

// The network-trial controller is loaded with this shell, before settings can
// change connectivity. Only unauthenticated pages avoid downloading it.
export function LazyRoot() {
  return (
    <Suspense fallback={null}>
      <Root />
    </Suspense>
  );
}
