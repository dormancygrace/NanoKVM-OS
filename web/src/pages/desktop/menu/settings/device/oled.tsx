import { useEffect, useRef, useState } from 'react';
import { Select, Switch } from 'antd';
import { ScreenShareOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

export const Oled = () => {
  const { t } = useTranslation();
  const [isOLEDExist, setIsOLEDExist] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [sleep, setSleep] = useState(-1);
  const [error, setError] = useState(false);
  const lastTimeout = useRef(60);

  useEffect(() => {
    let disposed = false;
    api
      .getOLED()
      .then((rsp) => {
        if (disposed) return;
        if (rsp.code !== 0) {
          setError(true);
          return;
        }
        setIsOLEDExist(rsp.data.exist);
        setSleep(rsp.data.sleep);
        if (rsp.data.sleep >= 0) lastTimeout.current = rsp.data.sleep;
      })
      .catch(() => {
        if (!disposed) setError(true);
      })
      .finally(() => {
        if (!disposed) setIsLoading(false);
      });
    return () => {
      disposed = true;
    };
  }, []);

  const options = [0, 15, 30, 60, 180, 300, 600, 1800, 3600].map((duration) => ({
    value: duration,
    label: t(`settings.device.oled.${duration}`)
  }));

  async function update(value: number) {
    if (isLoading) return;
    setIsLoading(true);
    setError(false);
    try {
      const rsp = await api.setOLED(value);
      if (rsp.code !== 0) {
        setError(true);
        return;
      }
      setSleep(value);
      if (value >= 0) lastTimeout.current = value;
    } catch {
      setError(true);
    } finally {
      setIsLoading(false);
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-4">
        <span>{t('settings.device.oled.title')}</span>
        {isOLEDExist || isLoading ? (
          <Switch
            aria-label={t('settings.device.oled.title')}
            checked={isOLEDExist && sleep >= 0}
            loading={isLoading}
            disabled={!isOLEDExist || isLoading}
            onChange={(enabled) => update(enabled ? lastTimeout.current : -1)}
          />
        ) : (
          <span className="text-neutral-500">
            <ScreenShareOff size={16} />
          </span>
        )}
      </div>
      {isOLEDExist && sleep >= 0 && (
        <div className="flex items-center justify-between gap-4">
          <span className="text-xs text-neutral-500">{t('settings.device.oled.description')}</span>
          <Select
            aria-label={t('settings.device.oled.description')}
            style={{ width: 150 }}
            value={sleep}
            options={options}
            disabled={isLoading}
            loading={isLoading}
            onChange={update}
          />
        </div>
      )}
      {error && (
        <div className="text-xs text-red-400" role="alert">
          {t('settings.device.oled.failed')}
        </div>
      )}
    </div>
  );
};
