import { Button } from 'antd';
import { useTranslation } from 'react-i18next';

// A failed optional chunk stays local to its content. Retry a temporary
// failure, or reload explicitly when an update removed the old asset URL.
export function LazyError({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation();
  return (
    <div role="alert" className="flex flex-wrap items-center gap-3 p-4">
      <span>{t('error.title')}</span>
      <Button onClick={onRetry}>{t('error.retry')}</Button>
      <Button onClick={() => window.location.reload()}>{t('error.refresh')}</Button>
    </div>
  );
}
