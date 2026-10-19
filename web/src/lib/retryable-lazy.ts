import { createElement, lazy, type ComponentProps, type ComponentType } from 'react';

// React caches rejected lazy imports. Keep successful pages stable, but let an
// explicit retry replace a failed import without reloading the desktop/session.
export function retryableLazy<T extends ComponentType<any>>(load: () => Promise<{ default: T }>) {
  let failed = false;
  const create = () =>
    lazy(async () => {
      try {
        return await load();
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
    retry() {
      if (!failed) return;
      failed = false;
      Page = create();
    }
  };
}
