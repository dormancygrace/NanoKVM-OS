import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Divider, Input, Modal, Popconfirm, Switch, Tag } from 'antd';
import { FileUpIcon, KeyRoundIcon, Trash2Icon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http.ts';

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
  const [error, setError] = useState('');
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
    }
  }, []);
  useEffect(() => {
    mounted.current = true;
    const poll = () => {
      if (!operation.current)
        void refresh().catch(() => {
          if (mounted.current) setError(t('vpn.loadFailed'));
        });
    };
    poll();
    const timer = window.setInterval(poll, 5000);
    return () => {
      mounted.current = false;
      window.clearInterval(timer);
    };
  }, [refresh, t]);
  async function run(action: () => Promise<{ code: number; msg: string }>) {
    if (operation.current) return false;
    operation.current = true;
    setBusy(true);
    setIsLocked(true);
    setError('');
    let ok = false;
    try {
      const rsp = await action();
      ok = rsp.code === 0;
      if (!ok) setError(rsp.msg);
    } catch {
      setError(t('vpn.requestFailed'));
    } finally {
      await refresh().catch(() => setError(t('vpn.requestFailed')));
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
      setError(t('vpn.openvpnLimit'));
      return;
    }
    const data = new FormData();
    Array.from(files).forEach((f) => data.append('files', f));
    void run(() => http.post('/api/extensions/openvpn/import', data));
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
    <div className="space-y-4">
      <div className="text-base">
        OpenVPN
        <VPNVersion name="openvpn" />
      </div>
      <Divider className="opacity-50" />
      <p className="text-sm text-neutral-400">{t('vpn.openvpnDescription')}</p>
      {available === false && <Alert type="warning" showIcon message={t('vpn.openvpnRequired')} />}
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
      <Button
        icon={<FileUpIcon size={16} />}
        loading={busy}
        disabled={available === undefined}
        onClick={() => input.current?.click()}
      >
        {t('vpn.openvpnImport')}
      </Button>
      {error && <Alert type="error" showIcon message={error} />}
      {available !== undefined && profiles.length === 0 && (
        <div className="rounded-lg border border-dashed border-neutral-700 p-6 text-center text-sm text-neutral-500">
          {t('vpn.openvpnEmpty')}
        </div>
      )}
      {profiles.map((p) => {
        const on = p.enabled || !['off', 'error'].includes(p.state);
        const otherOn = profiles.some(
          (other) => other.id !== p.id && (other.enabled || !['off', 'error'].includes(other.state))
        );
        return (
          <div
            key={p.id}
            className="space-y-2 rounded-lg border border-neutral-700/70 bg-neutral-800/40 p-4"
          >
            <div className="flex items-center justify-between gap-3">
              <div className="min-w-0 truncate font-medium" title={p.name}>
                {p.name}
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Tag
                  className="m-0"
                  color={
                    p.state === 'connected' ? 'green' : p.state === 'error' ? 'red' : undefined
                  }
                >
                  {t(`vpn.states.${p.state}`)}
                </Tag>
                <Switch
                  aria-label={`${t('vpn.enable')} ${p.name}`}
                  checked={on}
                  disabled={busy || !available || (!on && otherOn)}
                  onChange={(v) => change(p, v ? 'up' : 'down')}
                />
                {(p.needsAuth || p.needsPassphrase) && (
                  <Button
                    type="text"
                    size="small"
                    icon={<KeyRoundIcon size={15} />}
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
                    icon={<Trash2Icon size={15} />}
                    disabled={busy}
                    aria-label={`${t('vpn.delete')} ${p.name}`}
                  />
                </Popconfirm>
              </div>
            </div>
            {p.address && <div className="break-all text-xs text-neutral-400">{p.address}</div>}
            {p.state !== 'off' && (
              <div className="text-xs text-neutral-500">
                ↓ {(p.received / 1048576).toFixed(1)} MiB · ↑ {(p.sent / 1048576).toFixed(1)} MiB
              </div>
            )}
            {!p.credentialsSaved && (
              <div className="text-xs text-amber-400">{t('vpn.credentialsRequired')}</div>
            )}
            {p.error && (
              <div role="alert" className="text-xs text-red-400">
                {p.error}
              </div>
            )}
          </div>
        );
      })}
      <p className="text-xs text-neutral-500">{t('vpn.openvpnNote')}</p>
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
          <p className="text-sm text-neutral-400">{editing?.name}</p>
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
          {error && <Alert type="error" message={error} />}
        </div>
      </Modal>
    </div>
  );
}
