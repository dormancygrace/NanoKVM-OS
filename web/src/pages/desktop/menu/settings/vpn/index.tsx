import { useCallback, useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, message, Popconfirm, Switch, Tooltip } from 'antd';
import { CheckIcon, FileUpIcon, PencilIcon, Trash2Icon, XIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { http } from '@/lib/http.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import { HandshakeAge } from '@/components/handshake-age';
import { Panel, SettingRow, SettingsSection, StatusBadge } from '@/components/ui/settings.tsx';

import { profileTone } from './profile-tone.ts';
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
  const [loadFailed, setLoadFailed] = useState(false);
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
    if (operation.current) return;
    operation.current = true;
    setBusy(true);
    setIsLocked(true);
    try {
      const rsp = await action();
      if (rsp.code !== 0) showRequestError(rsp, 'vpn.requestFailed');
    } catch (err) {
      showRequestError(err, 'vpn.requestFailed');
    } finally {
      // A changed route may lose the response. Read back instead of replaying.
      await refresh().catch(() => {
        if (mounted.current) setLoadFailed(true);
      });
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
      void message.error(t('vpn.uploadLimit'));
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
    <div className="space-y-6">
      {loadFailed && <Alert type="error" showIcon message={t('vpn.loadFailed')} />}
      <SettingsSection>
        <VPNVersion name="wireguard" />
        <p className="text-fg-muted mt-0 text-sm">{t('vpn.description')}</p>
        <p className="text-fg-muted mt-0 text-xs">{t('vpn.routingNote')}</p>
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
        <div>
          <Button
            icon={<FileUpIcon size={16} />}
            loading={busy}
            disabled={available === undefined}
            onClick={() => input.current?.click()}
          >
            {t('vpn.import')}
          </Button>
        </div>
      </SettingsSection>

      <SettingsSection>
        {available !== undefined && profiles.length === 0 && (
          <Panel flush className="text-fg-muted border-dashed p-6 text-center text-sm">
            {t('vpn.empty')}
          </Panel>
        )}
        {profiles.length > 0 && (
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
                        <StatusBadge tone={profileTone(p.state)}>
                          {t(`vpn.states.${p.state}`)}
                        </StatusBadge>
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
                            icon={<Trash2Icon size={14} />}
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
                    <div className="text-fg-muted text-xs break-all">{p.address}</div>
                    {!!p.mtu && <div className="text-fg-muted text-xs">MTU {p.mtu}</div>}
                    <SettingRow label={t('vpn.routeAllowedIPs')}>
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
                    </SettingRow>
                    {p.state !== 'off' && (
                      <div className="text-fg-muted text-xs">
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
        <p className="text-fg-muted m-0 text-xs">{t('vpn.note')}</p>
      </SettingsSection>
    </div>
  );
}
