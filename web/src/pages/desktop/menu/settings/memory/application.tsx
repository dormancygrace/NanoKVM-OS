import { useEffect, useRef, useState } from 'react';
import { Alert, Switch, Tooltip } from 'antd';
import { CircleHelpIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

export const ApplicationMemory = () => {
  const { t } = useTranslation();
  const [isLoading, setIsLoading] = useState(true);
  const [isEnabled, setIsEnabled] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [limit, setLimit] = useState(75);
  const [error, setError] = useState('');
  const mounted = useRef(false);
  const changing = useRef(false);

  useEffect(() => {
    mounted.current = true;
    let cancelled = false;
    api
      .getMemoryLimit()
      .then((rsp) => {
        if (cancelled) return;
        if (rsp.code !== 0) throw new Error(rsp.msg || t('settings.memory.loadError'));
        setIsEnabled(!!rsp.data.enabled);
        setLimit(rsp.data.enabled ? rsp.data.limit : 75);
        setLoaded(true);
      })
      .catch((err) => {
        if (!cancelled)
          setError(err instanceof Error ? err.message : t('settings.memory.loadError'));
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });
    return () => {
      cancelled = true;
      mounted.current = false;
    };
  }, []);

  async function update(enabled: boolean) {
    if (!loaded || changing.current) return;
    changing.current = true;
    setIsLoading(true);
    setError('');
    try {
      const rsp = await api.setMemoryLimit(enabled, enabled ? 75 : 0);
      if (rsp.code !== 0) throw new Error(rsp.msg || t('settings.memory.applicationError'));
      if (mounted.current) {
        setIsEnabled(enabled);
        setLimit(75);
      }
    } catch (err) {
      if (mounted.current)
        setError(err instanceof Error ? err.message : t('settings.memory.applicationError'));
    } finally {
      changing.current = false;
      if (mounted.current) setIsLoading(false);
    }
  }

  return (
    <div>
      <div className="flex min-h-[40px] items-center justify-between space-x-6 rounded px-2 text-neutral-300">
        <div className="flex items-center space-x-1">
          <label htmlFor="memory-application-limit">{t('settings.memory.applicationTitle')}</label>
          <Tooltip
            title={t('settings.memory.applicationTip', { limit })}
            className="cursor-pointer text-neutral-500"
            placement="top"
            styles={{ root: { maxWidth: '400px' } }}
          >
            <CircleHelpIcon size={15} />
          </Tooltip>
        </div>
        <Switch
          id="memory-application-limit"
          value={isEnabled}
          loading={isLoading}
          disabled={!loaded || isLoading}
          size="small"
          onChange={update}
        />
      </div>
      {error && <Alert type="error" showIcon message={error} className="mt-2" />}
    </div>
  );
};
