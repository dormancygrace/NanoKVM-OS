import { useEffect, useRef, useState } from 'react';
import { Alert, Select, Switch } from 'antd';
import { ScreenShareOff } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';
import { LatestValueQueue } from '@/lib/latest-value-queue.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

const KEY_FAILED = 'settings.device.oled.failed';

export const Oled = () => {
  const { t } = useTranslation();
  const [isOLEDExist, setIsOLEDExist] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [sleep, setSleep] = useState(-1);
  const [loadError, setLoadError] = useState(false);
  const lastTimeout = useRef(60);
  const updateQueue = useRef<LatestValueQueue<number> | null>(null);

  if (!updateQueue.current) {
    updateQueue.current = new LatestValueQueue(async (value) => {
      try {
        const rsp = await api.setOLED(value);
        if (rsp.code !== 0) showRequestError(rsp, KEY_FAILED);
      } catch (err) {
        showRequestError(err, KEY_FAILED);
      }
    });
  }

  useEffect(() => {
    let disposed = false;
    api
      .getOLED()
      .then((rsp) => {
        if (disposed) return;
        if (rsp.code !== 0) {
          setLoadError(true);
          return;
        }
        setIsOLEDExist(rsp.data.exist);
        setSleep(rsp.data.sleep);
        if (rsp.data.sleep >= 0) lastTimeout.current = rsp.data.sleep;
      })
      .catch(() => {
        if (!disposed) setLoadError(true);
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

  function update(value: number) {
    if (isLoading) return;
    setSleep(value);
    if (value >= 0) lastTimeout.current = value;
    void updateQueue.current?.enqueue(value);
  }

  return (
    <div className="space-y-4">
      {loadError && <Alert type="error" showIcon message={t(KEY_FAILED)} />}
      <SettingRow label={t('settings.device.oled.title')} htmlFor="device-oled">
        {isOLEDExist || isLoading ? (
          <Switch
            id="device-oled"
            checked={isOLEDExist && sleep >= 0}
            loading={isLoading}
            disabled={!isOLEDExist || isLoading}
            onChange={(enabled) => update(enabled ? lastTimeout.current : -1)}
          />
        ) : (
          <span className="text-fg-muted">
            <ScreenShareOff size={16} />
          </span>
        )}
      </SettingRow>
      {isOLEDExist && sleep >= 0 && (
        <SettingRow label={t('settings.device.oled.description')} htmlFor="device-oled-sleep">
          <Select
            id="device-oled-sleep"
            style={{ width: 180 }}
            value={sleep}
            options={options}
            disabled={isLoading}
            loading={isLoading}
            onChange={update}
          />
        </SettingRow>
      )}
    </div>
  );
};
