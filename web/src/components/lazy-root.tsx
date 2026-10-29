import { Suspense } from 'react';
import { Spin } from 'antd';
import { ErrorBoundary } from 'react-error-boundary';

import { retryableLazy } from '@/lib/retryable-lazy';

import { LazyError } from './lazy-error';

const root = retryableLazy(() => import('./root').then(({ Root }) => ({ default: Root })));
const Root = root.Component;

// The network-trial controller is loaded with this shell, before settings can
// change connectivity. Only unauthenticated pages avoid downloading it.
export function LazyRoot() {
  return (
    <ErrorBoundary
      fallbackRender={({ resetErrorBoundary }) => (
        <LazyError
          onRetry={() => {
            root.retry();
            resetErrorBoundary();
          }}
        />
      )}
    >
      <Suspense
        fallback={
          <div className="flex h-screen items-center justify-center">
            <Spin size="large" />
          </div>
        }
      >
        <Root />
      </Suspense>
    </ErrorBoundary>
  );
}
