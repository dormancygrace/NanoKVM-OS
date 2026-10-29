import { lazy, Suspense, useState } from 'react';
import { Spin } from 'antd';
import { useTranslation } from 'react-i18next';

const TipsDialog = lazy(() => import('./tips-dialog'));

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
        <Suspense fallback={<Spin size="small" />}>
          <TipsDialog open={visible} onClose={() => setVisible(false)} />
        </Suspense>
      )}
    </>
  );
};
