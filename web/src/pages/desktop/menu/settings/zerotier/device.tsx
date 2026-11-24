import { useState } from 'react';
import { Alert, Button, Input, Popconfirm, Typography } from 'antd';
import { LogOutIcon, PlusIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/extensions/zerotier.ts';
import { SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

type DeviceProps = {
  status: api.ZerotierStatus;
  onChange: () => void;
};

type Tone = 'success' | 'warning' | 'danger' | 'neutral';

// ZeroTier's network states; anything else is shown as reported.
const networkTones: Record<string, Tone> = {
  OK: 'success',
  REQUESTING_CONFIGURATION: 'warning',
  AUTHENTICATION_REQUIRED: 'warning',
  ACCESS_DENIED: 'danger',
  NOT_FOUND: 'danger',
  PORT_ERROR: 'danger',
  CLIENT_TOO_OLD: 'danger'
};

export const Device = ({ status, onChange }: DeviceProps) => {
  const { t } = useTranslation();

  const [network, setNetwork] = useState('');
  const [busy, setBusy] = useState('');
  const [errMsg, setErrMsg] = useState('');
  const networks = status.networks ?? [];
  // Without a node ID the service runs but does not answer yet (it reads its
  // identity first): its networks are unknown, not none, and it cannot join.
  const answering = !!status.address;
  const id = network.trim().toLowerCase();
  const valid = answering && api.networkIdPattern.test(id);

  function request(key: string, call: () => ReturnType<typeof api.join>, onDone?: () => void) {
    if (busy) return;
    setBusy(key);
    setErrMsg('');
    call()
      .then((rsp) => {
        if (rsp.code !== 0) {
          setErrMsg(rsp.msg);
          return;
        }
        onDone?.();
      })
      .catch((err) => setErrMsg(err?.message || t('settings.zerotier.error.requestFailed')))
      .finally(() => {
        setBusy('');
        // A successful command does not prove the network's new state; ask
        // again instead of applying it optimistically.
        onChange();
      });
  }

  const networkState = (value: string) =>
    t(`settings.zerotier.networkStates.${value}`, { defaultValue: value });

  return (
    <div className="space-y-6">
      <SettingsSection>
        <SettingRow label={t('settings.zerotier.status')}>
          <StatusBadge tone={status.state === 'running' ? 'success' : 'warning'}>
            {t(
              status.state === 'running' ? 'settings.zerotier.online' : 'settings.zerotier.offline'
            )}
          </StatusBadge>
        </SettingRow>

        <SettingRow label={t('settings.zerotier.nodeId')} help={t('settings.zerotier.nodeIdHelp')}>
          {status.address ? (
            <Typography.Text copyable className="font-mono">
              {status.address}
            </Typography.Text>
          ) : (
            <span className="text-fg-muted">-</span>
          )}
        </SettingRow>

        <SettingRow label={t('settings.zerotier.version')}>
          <span>{status.version}</span>
        </SettingRow>
      </SettingsSection>

      <SettingsSection title={t('settings.zerotier.networks')}>
        {networks.length === 0 && (
          <p className="text-fg-muted px-1 text-sm">
            {t(answering ? 'settings.zerotier.noNetworks' : 'settings.zerotier.notAnswering')}
          </p>
        )}
        {networks.map((n) => (
          <SettingRow
            key={n.id}
            label={
              <span className="flex flex-wrap items-center gap-2">
                <span className="font-mono">{n.id}</span>
                {n.name && <span>{n.name}</span>}
                <StatusBadge tone={networkTones[n.status] ?? 'neutral'}>
                  {networkState(n.status)}
                </StatusBadge>
              </span>
            }
            description={
              n.addresses.length > 0
                ? n.addresses.join(', ')
                : n.status === 'ACCESS_DENIED'
                  ? t('settings.zerotier.authorizeHint')
                  : undefined
            }
          >
            <Popconfirm
              title={t('settings.zerotier.leaveConfirm')}
              description={t('settings.zerotier.leaveDesc')}
              okText={t('settings.zerotier.okBtn')}
              cancelText={t('settings.zerotier.cancelBtn')}
              placement="bottomRight"
              onConfirm={() => request('leave:' + n.id, () => api.leave(n.id))}
              disabled={!!busy}
            >
              <Button
                danger
                size="small"
                icon={<LogOutIcon size={14} />}
                loading={busy === 'leave:' + n.id}
                disabled={!!busy && busy !== 'leave:' + n.id}
              >
                {t('settings.zerotier.leave')}
              </Button>
            </Popconfirm>
          </SettingRow>
        ))}

        <SettingRow
          label={t('settings.zerotier.join')}
          description={t('settings.zerotier.joinDesc')}
          htmlFor="zerotier-network"
          stacked
        >
          <div className="flex gap-2">
            <Input
              id="zerotier-network"
              className="font-mono"
              value={network}
              placeholder="0123456789abcdef"
              status={answering && network && !valid ? 'error' : undefined}
              onChange={(e) => setNetwork(e.target.value)}
              onPressEnter={() =>
                valid &&
                request(
                  'join',
                  () => api.join(id),
                  () => setNetwork('')
                )
              }
            />
            <Button
              type="primary"
              icon={<PlusIcon size={16} />}
              disabled={!valid || (!!busy && busy !== 'join')}
              loading={busy === 'join'}
              onClick={() =>
                request(
                  'join',
                  () => api.join(id),
                  () => setNetwork('')
                )
              }
            >
              {t('settings.zerotier.joinBtn')}
            </Button>
          </div>
        </SettingRow>
      </SettingsSection>

      {errMsg && <Alert type="error" showIcon message={errMsg} />}
    </div>
  );
};
