import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Alert, Button, Popconfirm, Switch } from 'antd';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/netsvc.ts';
import type { ApiResponse } from '@/lib/http.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

export type ServiceActions = {
  // Saves the settings; false when the device refused them.
  save: (settings: unknown) => Promise<boolean>;
  saving: boolean;
  // Another change is being applied.
  busy: boolean;
};

type ServicePageProps<S extends api.ServiceStatus> = {
  name: api.ServiceName;
  // The settings of the service, shown while it is switched on.
  children: (status: S, actions: ServiceActions) => ReactNode;
};

type Busy = '' | 'switch' | 'save' | 'reset';

// One service of Settings > Services: a switch, off until it is switched on,
// and the settings of the service while it is on.
export function ServicePage<S extends api.ServiceStatus>({ name, children }: ServicePageProps<S>) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<S>();
  const [busy, setBusy] = useState<Busy>('');
  const [failed, setFailed] = useState(false);
  // The poll leaves the page alone while a change is being applied.
  const busyNow = useRef(false);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    const poll = async () => {
      if (busyNow.current) return;
      try {
        const rsp = await api.getStatus<S>(name);
        if (mounted.current && !busyNow.current && rsp.code === 0) setStatus(rsp.data);
      } catch {
        /* the last status stays on the page */
      }
    };
    void poll();
    const stop = pollWhileVisible(() => void poll(), 5000);
    return () => {
      mounted.current = false;
      stop();
    };
  }, [name]);

  const run = async (kind: Busy, call: () => Promise<ApiResponse<S>>) => {
    if (busyNow.current) return false;
    busyNow.current = true;
    setBusy(kind);
    setFailed(false);
    try {
      const rsp = await call();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      if (mounted.current) setStatus(rsp.data);
      return true;
    } catch {
      if (mounted.current) setFailed(true);
      return false;
    } finally {
      busyNow.current = false;
      if (mounted.current) setBusy('');
    }
  };

  if (!status) return null;

  const title = t(`settings.netServices.${name}.title`);
  const about = t(`settings.netServices.${name}.about`);

  // The packages are installed with the system. A device updated from a
  // release without them gets them from the server shortly after it starts,
  // and a package removed on the Software page stays removed.
  if (!status.installed) {
    return (
      <Alert
        type="warning"
        showIcon
        message={t('settings.netServices.notInstalled', { name: title })}
      />
    );
  }

  const switchId = `service-${name}-enabled`;
  return (
    <div className="space-y-6">
      <div className="text-fg-muted text-sm">{about}</div>
      {failed && <Alert type="error" showIcon message={t('settings.netServices.actionFailed')} />}
      <SettingsSection>
        <SettingRow
          label={t('settings.netServices.enabled')}
          htmlFor={switchId}
          description={
            status.enabled && (
              <span className="flex items-center gap-3">
                <StatusBadge tone={status.running ? 'success' : 'warning'}>
                  {status.running
                    ? t('settings.netServices.running')
                    : t('settings.netServices.notRunning')}
                </StatusBadge>
                {!status.running && (
                  <Button
                    size="small"
                    disabled={busy !== ''}
                    onClick={() => void run('switch', () => api.setEnabled<S>(name, true))}
                  >
                    {t('settings.netServices.start')}
                  </Button>
                )}
              </span>
            )
          }
        >
          <Switch
            id={switchId}
            checked={status.enabled}
            loading={busy === 'switch'}
            disabled={busy !== ''}
            onChange={(enabled) => void run('switch', () => api.setEnabled<S>(name, enabled))}
          />
        </SettingRow>
      </SettingsSection>
      {status.foreign && (
        <Alert
          type="warning"
          showIcon
          message={t('settings.netServices.foreign')}
          action={
            <Popconfirm
              title={t('settings.netServices.replaceConfirm', { name: title })}
              okText={t('settings.netServices.replace')}
              okButtonProps={{ danger: true }}
              onConfirm={() => void run('reset', () => api.resetConfig<S>(name))}
            >
              <Button size="small" loading={busy === 'reset'} disabled={busy !== ''}>
                {t('settings.netServices.replace')}
              </Button>
            </Popconfirm>
          }
        />
      )}
      {status.enabled &&
        !status.foreign &&
        children(status, {
          save: (settings) => run('save', () => api.saveSettings<S>(name, settings)),
          saving: busy === 'save',
          busy: busy !== ''
        })}
    </div>
  );
}
