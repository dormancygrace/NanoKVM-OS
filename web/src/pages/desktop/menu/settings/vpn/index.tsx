import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Divider, Input, Popconfirm, Switch, Tag, Tooltip } from 'antd';
import { CheckIcon, FileUpIcon, PencilIcon, Trash2Icon, XIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http.ts';
import { HandshakeAge } from '@/components/handshake-age';

import { VPNVersion } from './version';

type Profile = {
  id: string;
  name: string;
  state: 'off' | 'waiting' | 'connected' | 'idle' | 'error';
  enabled: boolean;
  routeAllowedIPs: boolean;
  address: string;
  lastHandshake: number;
  mtu?: number;
  received: number;
  sent: number;
  error?: string;
};

export function WireGuard({ setIsLocked }: { setIsLocked: (locked: boolean) => void }) {
  const { t } = useTranslation();
  const [serverNow, setServerNow] = useState<number>();
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [available, setAvailable] = useState<boolean>();
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [renameId, setRenameId] = useState<string | null>(null);
  const [nameDraft, setNameDraft] = useState('');
  const operation = useRef(false);
  const input = useRef<HTMLInputElement>(null);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    const rsp = await http.get('/api/extensions/wireguard/status');
    if (rsp.code !== 0) throw new Error(rsp.msg);
    if (mounted.current) {
      setProfiles(rsp.data.profiles);
      setServerNow(rsp.data.now);
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
    if (operation.current) return;
    operation.current = true;
    setBusy(true);
    setIsLocked(true);
    setError('');
    try {
      const rsp = await action();
      if (rsp.code !== 0) setError(rsp.msg);
    } catch {
      setError(t('vpn.requestFailed'));
    } finally {
      // A changed route may lose the response. Read back instead of replaying.
      await refresh().catch(() => setError(t('vpn.requestFailed')));
      operation.current = false;
      setBusy(false);
      setIsLocked(false);
    }
  }
  function change(profile: Profile, action: string) {
    void run(() => http.post('/api/extensions/wireguard/profile', { id: profile.id, action }));
  }
  function upload(files: FileList | null) {
    if (!files?.length) return;
    if (files.length + profiles.length > 16 || Array.from(files).some((f) => f.size > 65536)) {
      setError(t('vpn.uploadLimit'));
      return;
    }
    const data = new FormData();
    Array.from(files).forEach((f) => data.append('files', f));
    void run(() => http.post('/api/extensions/wireguard/import', data));
  }
  function rename(profile: Profile) {
    const name = nameDraft.trim();
    if (!name || Array.from(name).length > 128) return;
    void run(async () => {
      const rsp = await http.post('/api/extensions/wireguard/profile', {
        id: profile.id,
        action: 'rename',
        name
      });
      if (rsp.code === 0) setRenameId(null);
      return rsp;
    });
  }
  const formatBytes = (n: number) => `${(n / 1048576).toFixed(1)} MiB`;

  return (
    <div className="space-y-4">
      <div className="text-base">
        WireGuard
        <VPNVersion name="wireguard" />
      </div>
      <Divider className="opacity-50" />
      <p className="text-sm text-neutral-400">{t('vpn.description')}</p>
      <p className="text-xs text-neutral-500">{t('vpn.routingNote')}</p>
      {available === false && <Alert type="warning" showIcon message={t('vpn.systemRequired')} />}
      <input
        ref={input}
        type="file"
        accept=".conf"
        multiple
        hidden
        aria-label={t('vpn.import')}
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
        {t('vpn.import')}
      </Button>
      {error && <Alert type="error" showIcon message={error} />}
      {available !== undefined && profiles.length === 0 && (
        <div className="rounded-lg border border-dashed border-neutral-700 p-6 text-center text-sm text-neutral-500">
          {t('vpn.empty')}
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
              <div className="flex min-w-0 items-center gap-1">
                <div className="min-w-0 truncate font-medium" title={p.name}>
                  {p.name}
                </div>
                <Tooltip title={t('vpn.rename')}>
                  <Button
                    type="text"
                    size="small"
                    className="shrink-0"
                    aria-label={`${t('vpn.rename')} ${p.name}`}
                    icon={<PencilIcon size={14} />}
                    disabled={busy}
                    onClick={() => {
                      setRenameId(p.id);
                      setNameDraft(p.name);
                    }}
                  />
                </Tooltip>
              </div>
              <div className="flex shrink-0 items-center gap-3">
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
                  onChange={(value) => change(p, value ? 'up' : 'down')}
                />
                <Popconfirm
                  title={t('vpn.deleteConfirm')}
                  onConfirm={() => change(p, 'delete')}
                  disabled={busy}
                >
                  <Button
                    type="text"
                    size="small"
                    aria-label={`${t('vpn.delete')} ${p.name}`}
                    icon={<Trash2Icon size={15} />}
                    disabled={busy}
                  />
                </Popconfirm>
              </div>
            </div>
            {renameId === p.id && (
              <div className="flex items-center gap-1">
                <Input
                  autoFocus
                  aria-label={t('vpn.profileName')}
                  value={nameDraft}
                  maxLength={128}
                  disabled={busy}
                  onChange={(event) => setNameDraft(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === 'Escape') {
                      event.stopPropagation();
                      if (!busy) setRenameId(null);
                    }
                  }}
                  onPressEnter={() => rename(p)}
                />
                <Button
                  type="text"
                  aria-label={t('vpn.save')}
                  icon={<CheckIcon size={16} />}
                  disabled={busy || !nameDraft.trim()}
                  onClick={() => rename(p)}
                />
                <Button
                  type="text"
                  aria-label={t('vpn.cancelRename')}
                  icon={<XIcon size={16} />}
                  disabled={busy}
                  onClick={() => setRenameId(null)}
                />
              </div>
            )}
            <div className="break-all text-xs text-neutral-400">{p.address}</div>
            {!!p.mtu && <div className="text-xs text-neutral-500">MTU {p.mtu}</div>}
            <div className="flex items-center justify-between gap-3 text-sm text-neutral-300">
              <span>{t('vpn.routeAllowedIPs')}</span>
              <Tooltip title={on ? t('vpn.routingDisableFirst') : t('vpn.routingHelp')}>
                <span>
                  <Switch
                    size="small"
                    aria-label={`${t('vpn.routeAllowedIPs')} ${p.name}`}
                    checked={p.routeAllowedIPs ?? false}
                    disabled={busy || on}
                    onChange={(routeAllowedIPs) =>
                      void run(() =>
                        http.post('/api/extensions/wireguard/profile', {
                          id: p.id,
                          action: 'routing',
                          routeAllowedIPs
                        })
                      )
                    }
                  />
                </span>
              </Tooltip>
            </div>
            {p.state !== 'off' && (
              <div className="text-xs text-neutral-500">
                ↓ {formatBytes(p.received)} · ↑ {formatBytes(p.sent)}
                {p.lastHandshake > 0 && (
                  <>
                    {' '}
                    · {t('vpn.handshake')}{' '}
                    <HandshakeAge timestamp={p.lastHandshake} serverNow={serverNow} />
                  </>
                )}
              </div>
            )}
            {p.error && (
              <div role="alert" className="text-xs text-red-400">
                {p.error}
              </div>
            )}
          </div>
        );
      })}
      <p className="text-xs text-neutral-500">{t('vpn.note')}</p>
    </div>
  );
}
