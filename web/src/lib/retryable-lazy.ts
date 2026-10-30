import { createElement, lazy, type ComponentProps, type ComponentType } from 'react';

// React caches rejected lazy imports. Keep successful pages stable, but let an
// explicit retry replace a failed import without reloading the desktop/session.
export function retryableLazy<T extends ComponentType<any>>(load: () => Promise<{ default: T }>) {
  let failed = false;
  let rendering = false;
  let pending: ReturnType<typeof load> | undefined;
  const request = () => (pending ??= Promise.resolve().then(load));
  const create = () =>
    lazy(async () => {
      rendering = true;
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
    // A speculative failure must not poison the first open. Once rendering
    // starts, preserve its failure for the local boundary and explicit retry.
    preload() {
      const attempt = request();
      return attempt.then(
        () => undefined,
        () => {
          if (!rendering && pending === attempt) pending = undefined;
        }
      );
    },
    retry() {
      if (!failed) return;
      failed = false;
      rendering = false;
      pending = undefined;
      Page = create();
    }
  };
}
