import { createElement, lazy, type ComponentProps, type ComponentType } from 'react';

// React caches rejected lazy imports. Keep successful pages stable, but let an
// explicit retry replace a failed import without reloading the desktop/session.
export function retryableLazy<T extends ComponentType<any>>(load: () => Promise<{ default: T }>) {
  let failed = false;
  let pending: ReturnType<typeof load> | undefined;
  const request = () => (pending ??= Promise.resolve().then(load));
  const create = () =>
    lazy(async () => {
      try {
        return await request();
      } catch (error) {
        failed = true;
        throw error;
      }
    });
  let Page = create();
  function Component(props: ComponentProps<T>) {
    return createElement(Page, props);
  }
  return {
    Component,
    // Best-effort warming must not raise an unhandled rejection. Keep any
    // failure for the local error boundary when the user actually opens it.
    preload() {
      return request().then(
        () => undefined,
        () => undefined
      );
    },
    retry() {
      if (!failed) return;
      failed = false;
      pending = undefined;
      Page = create();
    }
  };
}
