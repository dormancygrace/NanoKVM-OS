import { useEffect, useState } from 'react';
import { Select, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { setMouseJiggler } from '@/api/vm.ts';
import { showRequestError } from '@/lib/show-request-error.ts';

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
    <div className="flex items-center justify-between">
      <div className="flex flex-col space-y-1">
        <span>{t('settings.device.mouseJiggler.title')}</span>
        <span className="text-fg-muted text-xs">
          {t('settings.device.mouseJiggler.description')}
        </span>
      </div>

      {enabled ? (
        <Select
          aria-label={t('settings.device.mouseJiggler.title')}
          style={{ width: 150 }}
          value={mode}
          options={options}
          loading={isLoading}
          onChange={updateMode}
        />
      ) : (
        <Switch
          aria-label={t('settings.device.mouseJiggler.title')}
          checked={enabled}
          loading={isLoading}
          onChange={enable}
        />
      )}
    </div>
  );
};
