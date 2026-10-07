import { useEffect, useState } from 'react';
import { Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { setMouseJiggler } from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

export const MouseJiggler = () => {
  const { t } = useTranslation();

  const [enabled, setEnabled] = useState(false);
  const [mode, setMode] = useState('relative');
  const [isLoading, setIsLoading] = useState(false);

  useEffect(() => {
    getMouseJiggler();
  }, []);

  const options = [
    { value: 'disable', label: t('settings.device.mouseJiggler.disable') },
    { value: 'relative', label: t('settings.device.mouseJiggler.relative') },
    { value: 'absolute', label: t('settings.device.mouseJiggler.absolute') }
  ];

  function getMouseJiggler() {
    setIsLoading(true);

    api
      .getMouseJiggler()
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }

        setEnabled(rsp.data.enabled);
        setMode(rsp.data.mode);
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsLoading(false);
      });
  }

  function enable() {
    if (isLoading) return;
    setIsLoading(true);

    api
      .setMouseJiggler(true, mode)
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }

        setEnabled(true);
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsLoading(false);
      });
  }

  function updateMode(value: string) {
    if (isLoading) return;
    setIsLoading(true);

    const _enabled = value !== 'disable';
    const _mode = value === 'disable' ? 'relative' : value;

    setMouseJiggler(_enabled, _mode)
      .then((rsp) => {
        if (rsp.code !== 0) {
          showRequestError(rsp);
          return;
        }

        setEnabled(_enabled);
        setMode(_mode);
      })
      .catch((err) => showRequestError(err))
      .finally(() => {
        setIsLoading(false);
      });
  }

  return (
    <SettingRow
      label={t('settings.device.mouseJiggler.title')}
      description={t('settings.device.mouseJiggler.description')}
      htmlFor="device-mouse-jiggler"
    >
      {enabled ? (
        <Select
          id="device-mouse-jiggler"
          aria-describedby="device-mouse-jiggler-description"
          style={{ width: 180 }}
          value={mode}
          options={options}
          loading={isLoading}
          onChange={updateMode}
        />
      ) : (
        <Switch
          id="device-mouse-jiggler"
          aria-describedby="device-mouse-jiggler-description"
          checked={enabled}
          loading={isLoading}
          onChange={enable}
        />
      )}
    </SettingRow>
  );
};
