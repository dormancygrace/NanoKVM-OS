import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Alert, Button, Popconfirm, Result, Switch } from 'antd';
import { isAxiosError } from 'axios';
import { CircleAlertIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/netsvc.ts';
import type { ApiResponse } from '@/lib/http.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

import { ExtensionInstallResult } from '../vpn/extension-install-result.tsx';

export type ServiceActions = {
  // Saves the settings; false when the device refused them.
  save: (settings: unknown) => Promise<boolean>;
  saving: boolean;
  // Another change is being applied.
  busy: boolean;
};

type ServicePageProps<S extends api.ServiceStatus> = {
  name: api.ServiceName;
  setIsLocked: (isLocked: boolean) => void;
  // The settings of the service, shown while it is switched on.
  children: (status: S, actions: ServiceActions) => ReactNode;
};

type Busy = '' | 'install' | 'uninstall' | 'switch' | 'save';

// One optional service of Settings > Services: an install button until its
// package is installed, then a switch, and its settings while it is on.
export function ServicePage<S extends api.ServiceStatus>({
  name,
  setIsLocked,
  children
}: ServicePageProps<S>) {
  const { t } = useTranslation();
  const [status, setStatus] = useState<S>();
  const [busy, setBusy] = useState<Busy>('');
  const [error, setError] = useState<'' | 'install' | 'action'>('');
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
    setError('');
    try {
      const rsp = await call();
      if (rsp.code !== 0) throw new Error(rsp.msg);
      if (mounted.current) setStatus(rsp.data);
      return true;
    } catch (err) {
      // An install that outlived its request may still be running on the
      // device: wait for it, then show what it did.
      const finished =
        kind === 'install' && isAxiosError(err) && !err.response
          ? await installFinished()
          : undefined;
      if (!mounted.current) return false;
      if (finished) {
        setStatus(finished);
        return true;
      }
      setError(kind === 'install' ? 'install' : 'action');
      return false;
    } finally {
      busyNow.current = false;
      if (mounted.current) setBusy('');
    }
  };

  async function installFinished() {
    const deadline = Date.now() + 15 * 60 * 1000;
    while (Date.now() < deadline && mounted.current) {
      await new Promise((resolve) => setTimeout(resolve, 3000));
      try {
        const rsp = await api.getStatus<S>(name);
        if (rsp.code === 0 && rsp.data.installed) return rsp.data;
      } catch {
        /* the device may be busy; ask again */
      }
    }
    return undefined;
  }

  const install = async () => {
    setIsLocked(true);
    await run('install', () => api.install<S>(name));
    setIsLocked(false);
  };

  if (!status) return null;

  const title = t(`settings.netServices.${name}.title`);
  const about = t(`settings.netServices.${name}.about`);

  if (!status.installed) {
    if (error === 'install') {
      return (
        <Result
          status="warning"
          title={t('settings.netServices.installFailed')}
          subTitle={t('settings.netServices.installRetry')}
          icon={<CircleAlertIcon size={72} />}
          extra={
            <Button type="primary" onClick={install}>
              {t('settings.netServices.install')}
            </Button>
          }
        />
      );
    }
    return (
      <ExtensionInstallResult
        title={t('settings.netServices.notInstalled', { name: title })}
        description={`${about} ${t('settings.software.community.vpnNote')}`}
        actionLabel={
          busy === 'install'
            ? t('settings.netServices.installing')
            : t('settings.netServices.install')
        }
        loading={busy === 'install'}
        onInstall={install}
      />
    );
  }

  const switchId = `service-${name}-enabled`;
  return (
    <div className="space-y-6">
      <div className="text-fg-muted text-sm">{about}</div>
      {error === 'action' && (
        <Alert type="error" showIcon message={t('settings.netServices.actionFailed')} />
      )}
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
      {status.enabled &&
        children(status, {
          save: (settings) => run('save', () => api.saveSettings<S>(name, settings)),
          saving: busy === 'save',
          busy: busy !== ''
        })}
      <SettingsSection>
        <SettingRow
          label={t('settings.netServices.uninstall')}
          description={t('settings.netServices.uninstallHint')}
        >
          <Popconfirm
            title={t('settings.netServices.uninstallConfirm', { name: title })}
            okText={t('settings.netServices.uninstall')}
            okButtonProps={{ danger: true }}
            onConfirm={() => void run('uninstall', () => api.uninstall<S>(name))}
          >
            <Button danger loading={busy === 'uninstall'} disabled={busy !== ''}>
              {t('settings.netServices.uninstall')}
            </Button>
          </Popconfirm>
        </SettingRow>
      </SettingsSection>
    </div>
  );
}
