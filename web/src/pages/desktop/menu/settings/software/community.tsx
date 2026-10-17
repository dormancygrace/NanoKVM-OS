import { useEffect, useState } from 'react';
import { Alert, message, Spin, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http';
import { Panel } from '@/components/ui/settings.tsx';

type Status = { enabled: boolean; available: boolean };

type CommunityProps = {
  disabled?: boolean;
  // Called after the repository is switched on, to refresh the indexes.
  onEnabled: () => void;
};

export const CommunityRepository = ({ disabled, onEnabled }: CommunityProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.software.community' });
  const [status, setStatus] = useState<Status>();
  const [needed, setNeeded] = useState<string[]>([]);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let active = true;
    http
      .get('/api/os/update/software/community')
      .then((rsp) => {
        if (!active) return;
        if (rsp.code !== 0) throw new Error(rsp.msg);
        setStatus(rsp.data);
      })
      .catch(() => {
        if (active) setError(t('failed'));
      });
    return () => {
      active = false;
    };
  }, [t]);

  const change = async (enabled: boolean) => {
    if (busy || !status) return;
    setBusy(true);
    setNeeded([]);
    try {
      const rsp = await http.request({
        method: 'put',
        url: '/api/os/update/software/community',
        data: { enabled }
      });
      if (rsp.code !== 0) throw new Error(rsp.msg);
      setStatus({ ...status, enabled: rsp.data.enabled });
      setError('');
      if (rsp.data.needed?.length) {
        setNeeded(rsp.data.needed);
      } else {
        void message.success(enabled ? t('enabled') : t('disabled'));
        if (enabled) onEnabled();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('failed'));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Panel className="space-y-3">
      {error && <Alert type="error" title={error} showIcon />}
      {!status && !error ? (
        <Spin size="small" />
      ) : (
        <>
          <label className="flex items-center gap-3">
            <Switch
              checked={status?.enabled}
              loading={busy}
              disabled={disabled || !status?.available}
              onChange={change}
            />
            <span>{t('label')}</span>
          </label>
          <p className="text-fg-muted m-0 text-xs">
            {status?.available === false ? t('unavailable') : t('hint')}
          </p>
          {needed.length > 0 && (
            <Alert type="warning" showIcon title={t('needed', { packages: needed.join(', ') })} />
          )}
        </>
      )}
    </Panel>
  );
};
