import { Suspense, useState } from 'react';
import { Spin } from 'antd';
import { ErrorBoundary } from 'react-error-boundary';
import { useTranslation } from 'react-i18next';

import { retryableLazy } from '@/lib/retryable-lazy';
import { LazyError } from '@/components/lazy-error';

const tips = retryableLazy(() => import('./tips-dialog'));
const TipsDialog = tips.Component;

export const Tips = () => {
  const { t } = useTranslation();
  const [opened, setOpened] = useState(false);
  const [visible, setVisible] = useState(false);
  return (
    <>
      <button
        type="button"
        className="nanokvm-button-base cursor-pointer text-neutral-300 underline underline-offset-4"
        onClick={() => {
          setOpened(true);
          setVisible(true);
        }}
      >
        {t('auth.forgetPassword')}
      </button>
      {opened && (
        <ErrorBoundary
          fallbackRender={({ resetErrorBoundary }) => (
            <LazyError
              onRetry={() => {
                tips.retry();
                resetErrorBoundary();
              }}
            />
          )}
        >
          <Suspense fallback={<Spin size="small" />}>
            <TipsDialog open={visible} onClose={() => setVisible(false)} />
          </Suspense>
        </ErrorBoundary>
      )}
    </>
  );
};
