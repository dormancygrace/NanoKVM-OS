import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, message, Modal, Popconfirm, Switch } from 'antd';
import { FileUpIcon, KeyRoundIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

import { ExtensionInstallResult } from './extension-install-result.tsx';
import { profileTone } from './profile-tone.ts';
import { VPNVersion } from './version';

type Profile = {
  id: string;
  name: string;
  state: string;
  enabled: boolean;
  needsAuth: boolean;
  needsPassphrase: boolean;
  credentialsSaved: boolean;
  address: string;
  received: number;
  sent: number;
  error?: string;
};

export function OpenVPN({ setIsLocked }: { setIsLocked: (locked: boolean) => void }) {
  const { t } = useTranslation();
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [available, setAvailable] = useState<boolean>();
  const [loadFailed, setLoadFailed] = useState(false);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<Profile>();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [passphrase, setPassphrase] = useState('');
  const operation = useRef(false);
  const mounted = useRef(true);
  const input = useRef<HTMLInputElement>(null);
  const refresh = useCallback(async () => {
    const rsp = await http.get('/api/extensions/openvpn/status');
    if (rsp.code !== 0) throw new Error(rsp.msg);
    if (mounted.current) {
      setProfiles(rsp.data.profiles);
      setAvailable(rsp.data.available);
      setLoadFailed(false);
    }
  }, []);
  useEffect(() => {
    mounted.current = true;
    const poll = () => {
      if (!operation.current)
        void refresh().catch(() => {
          if (mounted.current) setLoadFailed(true);
        });
    };
    poll();
    const stopPolling = pollWhileVisible(poll, 5000);
    return () => {
      mounted.current = false;
      stopPolling();
    };
  }, [refresh]);
  async function run(action: () => Promise<{ code: number; msg: string }>) {
    if (operation.current) return false;
    operation.current = true;
    setBusy(true);
    setIsLocked(true);
    let ok = false;
    try {
      const rsp = await action();
      ok = rsp.code === 0;
      if (!ok) showRequestError(rsp, 'vpn.requestFailed');
    } catch (err) {
      showRequestError(err, 'vpn.requestFailed');
    } finally {
      await refresh().catch(() => {
        if (mounted.current) setLoadFailed(true);
      });
      operation.current = false;
      setBusy(false);
      setIsLocked(false);
    }
    return ok;
  }
  function clearCredentials() {
    setEditing(undefined);
    setUsername('');
    setPassword('');
    setPassphrase('');
  }
  function change(p: Profile, action: string) {
    if (action === 'up' && !p.credentialsSaved) {
      setEditing(p);
      return;
    }
    void run(() => http.post('/api/extensions/openvpn/profile', { id: p.id, action }));
  }
  function upload(files: FileList | null) {
    if (!files?.length) return;
    if (files.length > 64 || Array.from(files).some((f) => f.size > 262144)) {
      void message.error(t('vpn.openvpnLimit'));
      return;
    }
    const data = new FormData();
    Array.from(files).forEach((f) => data.append('files', f));
    void run(() => http.post('/api/extensions/openvpn/import', data));
  }
  function install() {
    void run(() => http.post('/api/extensions/openvpn/install'));
  }
  function uninstall() {
    void run(() => http.post('/api/extensions/openvpn/uninstall'));
  }
  async function saveCredentials() {
    if (!editing) return;
    if (
      await run(() =>
        http.post('/api/extensions/openvpn/profile', {
          id: editing.id,
          action: 'credentials',
          username,
          password,
          passphrase
        })
      )
    )
      clearCredentials();
  }
  return (
    <div className="space-y-6">
      {loadFailed && <Alert type="error" showIcon message={t('vpn.loadFailed')} />}
      <SettingsSection>
        <VPNVersion name="openvpn" />
        <p className="text-fg-muted mt-0 text-sm">{t('vpn.openvpnDescription')}</p>
        {available === false && (
          <ExtensionInstallResult
            title={t('vpn.openvpnNotInstalled')}
            description={t('vpn.openvpnInstallDescription')}
            actionLabel={t('vpn.openvpnInstall')}
            loading={busy}
            onInstall={install}
          />
        )}
        {available === true && (
          <SettingRow label={<StatusBadge tone="success">{t('vpn.openvpnInstalled')}</StatusBadge>}>
            <Popconfirm
              title={t('vpn.openvpnUninstall')}
              description={t('vpn.openvpnUninstallWarning')}
              onConfirm={uninstall}
              okText={t('vpn.confirm')}
              cancelText={t('vpn.cancel')}
              disabled={busy}
            >
              <Button danger loading={busy} icon={<Trash2Icon size={16} />}>
                {t('vpn.openvpnUninstall')}
              </Button>
            </Popconfirm>
          </SettingRow>
        )}
        <input
          ref={input}
          type="file"
          multiple
          hidden
          aria-label={t('vpn.openvpnImport')}
          onChange={(e) => {
            upload(e.target.files);
            e.target.value = '';
          }}
        />
        <div>
          <Button
            icon={<FileUpIcon size={16} />}
            loading={busy}
            disabled={available !== true}
            onClick={() => input.current?.click()}
          >
            {t('vpn.openvpnImport')}
          </Button>
        </div>
      </SettingsSection>

      <SettingsSection>
        {available === true && profiles.length === 0 && (
          <Panel flush className="text-fg-muted border-dashed p-6 text-center text-sm">
            {t('vpn.openvpnEmpty')}
          </Panel>
        )}
        {available === true && profiles.length > 0 && (
          <Panel flush>
            <ul className="divide-line m-0 list-none divide-y p-0">
              {profiles.map((p) => {
                const on = p.enabled || !['off', 'error'].includes(p.state);
                const otherOn = profiles.some(
                  (other) =>
                    other.id !== p.id && (other.enabled || !['off', 'error'].includes(other.state))
                );
                return (
                  <li key={p.id} className="space-y-2 px-4 py-3">
                    <div className="flex items-center justify-between gap-3">
                      <div className="min-w-0 truncate font-medium" title={p.name}>
                        {p.name}
                      </div>
                      <div className="flex shrink-0 items-center gap-2">
                        <StatusBadge tone={profileTone(p.state)}>
                          {t(`vpn.states.${p.state}`)}
                        </StatusBadge>
                        <Switch
                          aria-label={`${t('vpn.enable')} ${p.name}`}
                          checked={on}
                          disabled={busy || available !== true || (!on && otherOn)}
                          onChange={(v) => change(p, v ? 'up' : 'down')}
                        />
                        {(p.needsAuth || p.needsPassphrase) && (
                          <Button
                            type="text"
                            size="small"
                            icon={<KeyRoundIcon size={14} />}
                            aria-label={`${t('vpn.credentials')} ${p.name}`}
                            disabled={busy || on}
                            onClick={() => setEditing(p)}
                          />
                        )}
                        <Popconfirm
                          title={t('vpn.deleteConfirm')}
                          onConfirm={() => change(p, 'delete')}
                          disabled={busy}
                        >
                          <Button
                            type="text"
                            size="small"
                            icon={<Trash2Icon size={14} />}
                            disabled={busy}
                            aria-label={`${t('vpn.delete')} ${p.name}`}
                          />
                        </Popconfirm>
                      </div>
                    </div>
                    {p.address && (
                      <div className="text-fg-muted text-xs break-all">{p.address}</div>
                    )}
                    {p.state !== 'off' && (
                      <div className="text-fg-muted text-xs">
                        ↓ {(p.received / 1048576).toFixed(1)} MiB · ↑{' '}
                        {(p.sent / 1048576).toFixed(1)} MiB
                      </div>
                    )}
                    {!p.credentialsSaved && (
                      <div className="text-warning text-xs">{t('vpn.credentialsRequired')}</div>
                    )}
                    {p.error && (
                      <div role="alert" className="text-danger text-xs">
                        {p.error}
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          </Panel>
        )}
        <p className="text-fg-muted m-0 text-xs">{t('vpn.openvpnNote')}</p>
      </SettingsSection>
      <Modal
        title={t('vpn.credentials')}
        open={!!editing}
        onCancel={clearCredentials}
        onOk={() => void saveCredentials()}
        confirmLoading={busy}
        cancelButtonProps={{ disabled: busy }}
        closable={!busy}
        maskClosable={!busy}
        keyboard={!busy}
        okText={t('vpn.save')}
        destroyOnHidden
      >
        <div className="space-y-3 py-4">
          <p className="text-fg-muted text-sm">{editing?.name}</p>
          {editing?.needsAuth && (
            <>
              <Input
                aria-label={t('vpn.username')}
                placeholder={t('vpn.username')}
                value={username}
                autoComplete="off"
                onChange={(e) => setUsername(e.target.value)}
              />
              <Input.Password
                aria-label={t('vpn.password')}
                placeholder={t('vpn.password')}
                value={password}
                autoComplete="new-password"
                onChange={(e) => setPassword(e.target.value)}
              />
            </>
          )}
          {editing?.needsPassphrase && (
            <Input.Password
              aria-label={t('vpn.passphrase')}
              placeholder={t('vpn.passphrase')}
              value={passphrase}
              autoComplete="new-password"
              onChange={(e) => setPassphrase(e.target.value)}
            />
          )}
        </div>
      </Modal>
    </div>
  );
}
