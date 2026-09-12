import { Collapse } from 'antd';
import { useTranslation } from 'react-i18next';

import { CPUFrequency } from './cpu-frequency.tsx';

const children = (
  <div className="space-y-6 py-3">
    <CPUFrequency />
    {/*<Autostart />*/}
  </div>
);

export const Advanced = () => {
  const { t } = useTranslation();

  return (
    <Collapse
      ghost
      expandIconPosition="end"
      items={[{ key: 'advanced', label: t('settings.device.advanced'), children }]}
    />
  );
};
