import { useEffect, useState } from 'react';
import { Alert, Button, Input, Segmented } from 'antd';
import { CheckIcon, PlusIcon, XIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/network.ts';
import type { DTLSCipher, WebRTCServers as State, StunMode } from '@/api/network.ts';
import { validIceAddress } from '@/lib/ice-address.ts';
import { requestErrorText } from '@/lib/request-error.ts';
import { showRequestError } from '@/lib/show-request-error.ts';
import { Panel, SettingRow, SettingsSection } from '@/components/ui/settings.tsx';

const maxServers = 4;

const noSpaces = (value: string) => /^[\x21-\x7e]+$/.test(value);

/*
 * The STUN and TURN servers that WebRTC video, audio and RustDesk use to
 * reach the device across NAT, and the DTLS cipher offered first. Saved in
 * server.yaml; new connections use them at once. This settings response does not expose the TURN password: an empty field
 * keeps the stored one.
 */
export const WebRTCServers = () => {
  const { t } = useTranslation();
  const [saved, setSaved] = useState<State | null>(null);
  const [mode, setMode] = useState<StunMode>('default');
  const [servers, setServers] = useState<string[]>([]);
  const [turnAddress, setTurnAddress] = useState('');
  const [turnUser, setTurnUser] = useState('');
  const [turnPassword, setTurnPassword] = useState('');
  const [cipher, setCipher] = useState<DTLSCipher>('chacha20');
  const [isSaving, setIsSaving] = useState(false);
  const [loadError, setLoadError] = useState<{ error: unknown } | null>(null);
  const [message, setMessage] = useState('');

  function adopt(state: State) {
    setSaved(state);
    setMode(state.stunMode);
    setServers(state.stunMode === 'custom' ? state.stun : []);
    setTurnAddress(state.turn.address);
    setTurnUser(state.turn.username);
    setTurnPassword('');
    setCipher(state.dtlsCipher === 'aes-gcm' ? 'aes-gcm' : 'chacha20');
  }

  useEffect(() => {
    let active = true;
    api
      .getWebRTCServers()
      .then((rsp) => {
        if (!active) return;
        if (rsp.code !== 0) setLoadError({ error: rsp });
        else adopt(rsp.data);
      })
      .catch((error: unknown) => active && setLoadError({ error }));
    return () => {
      active = false;
    };
  }, []);

  const entered = servers.map((s) => s.trim()).filter(Boolean);
  const invalidStun = mode === 'custom' && entered.some((s) => !validIceAddress(s));
  const missingStun = mode === 'custom' && entered.length === 0;
  const turnOn = turnAddress.trim() !== '';
  const invalidTurn =
    turnOn &&
    (!validIceAddress(turnAddress) ||
      !noSpaces(turnUser) ||
      (turnPassword !== '' && !noSpaces(turnPassword)) ||
      (turnPassword === '' && !saved?.turn.hasCredential));
  const changed =
    !!saved &&
    (mode !== saved.stunMode ||
      (mode === 'custom' && entered.join(',') !== saved.stun.join(',')) ||
      turnAddress.trim() !== saved.turn.address ||
      (turnOn && turnUser !== saved.turn.username) ||
      turnPassword !== '' ||
      cipher !== saved.dtlsCipher);
  const blocked = invalidStun || missingStun || invalidTurn;

  function edit() {
    setMessage('');
  }

  async function save() {
    if (isSaving || blocked) return;
    setIsSaving(true);
    setMessage('');
    try {
      const rsp = await api.setWebRTCServers({
        stunMode: mode,
        stun: mode === 'custom' ? entered : [],
        turn: {
          address: turnAddress.trim(),
          username: turnOn ? turnUser : '',
          credential: turnPassword
        },
        dtlsCipher: cipher
      });
      if (rsp.code !== 0) {
        showRequestError(rsp, 'settings.network.webrtc.saveFailed');
        return;
      }
      adopt(rsp.data as State);
      setMessage(t('settings.network.webrtc.saved'));
    } catch (error) {
      showRequestError(error, 'settings.network.webrtc.saveFailed');
    } finally {
      setIsSaving(false);
    }
  }

  const loadErrorText = loadError && requestErrorText(loadError.error, t('error.requestFailed'));
  const disabled = !saved || isSaving;
  const problem = invalidStun
    ? t('settings.network.webrtc.invalid')
    : missingStun
      ? t('settings.network.webrtc.noServers')
      : invalidTurn
        ? t('settings.network.webrtc.turnIncomplete')
        : '';
  const statusText = problem || message || (changed ? t('settings.network.webrtc.unsaved') : '');
  const statusColor = problem ? 'text-danger' : message ? 'text-success' : 'text-warning';

  return (
    <SettingsSection
      title={t('settings.network.webrtc.title')}
      description={t('settings.network.webrtc.description')}
    >
      {loadErrorText && <Alert type="error" showIcon title={loadErrorText} />}

      <SettingRow
        label={t('settings.network.webrtc.stun')}
        description={
          mode === 'default'
            ? t('settings.network.webrtc.stunDefault', {
                servers: saved?.defaultStun.join(', ') ?? ''
              })
            : mode === 'off'
              ? t('settings.network.webrtc.stunOffHint')
              : t('settings.network.webrtc.stunCustomHint')
        }
      >
        <Segmented
          disabled={disabled}
          value={mode}
          onChange={(value) => {
            const next = value as StunMode;
            setMode(next);
            if (next === 'custom' && servers.length === 0) setServers(saved?.defaultStun ?? ['']);
            edit();
          }}
          options={[
            { label: t('settings.network.webrtc.modeDefault'), value: 'default' },
            { label: t('settings.network.webrtc.modeCustom'), value: 'custom' },
            { label: t('settings.network.webrtc.modeOff'), value: 'off' }
          ]}
        />
      </SettingRow>

      {mode === 'custom' && (
        <Panel className="space-y-2">
          {servers.map((server, index) => (
            <div key={index} className="flex items-center gap-2">
              <Input
                aria-label={t('settings.network.webrtc.server', { index: index + 1 })}
                value={server}
                disabled={disabled}
                placeholder="stun.example.com:3478"
                status={server.trim() && !validIceAddress(server) ? 'error' : undefined}
                onChange={(e) => {
                  const next = [...servers];
                  next[index] = e.target.value;
                  setServers(next);
                  edit();
                }}
              />
              <Button
                size="small"
                shape="circle"
                aria-label={t('settings.network.webrtc.remove', { index: index + 1 })}
                title={t('settings.network.webrtc.remove', { index: index + 1 })}
                icon={<XIcon size={14} />}
                disabled={disabled}
                onClick={() => {
                  setServers(servers.filter((_, i) => i !== index));
                  edit();
                }}
              />
            </div>
          ))}
          {servers.length < maxServers && (
            <Button
              type="dashed"
              block
              icon={<PlusIcon size={14} />}
              disabled={disabled}
              onClick={() => {
                setServers([...servers, '']);
                edit();
              }}
            >
              {t('settings.network.webrtc.add')}
            </Button>
          )}
        </Panel>
      )}

      <SettingRow
        label={t('settings.network.webrtc.turn')}
        description={t('settings.network.webrtc.turnHint')}
        stacked
      >
        <Panel className="space-y-2">
          <Input
            aria-label={t('settings.network.webrtc.turnAddress')}
            placeholder={t('settings.network.webrtc.turnAddressPlaceholder')}
            value={turnAddress}
            disabled={disabled}
            autoComplete="off"
            status={turnOn && !validIceAddress(turnAddress) ? 'error' : undefined}
            onChange={(e) => {
              setTurnAddress(e.target.value);
              edit();
            }}
          />
          {turnOn && (
            <>
              <Input
                aria-label={t('settings.network.webrtc.turnUser')}
                placeholder={t('settings.network.webrtc.turnUser')}
                value={turnUser}
                disabled={disabled}
                autoComplete="off"
                onChange={(e) => {
                  setTurnUser(e.target.value);
                  edit();
                }}
              />
              <Input.Password
                aria-label={t('settings.network.webrtc.turnPassword')}
                placeholder={
                  saved?.turn.hasCredential
                    ? t('settings.network.webrtc.turnPasswordKept')
                    : t('settings.network.webrtc.turnPassword')
                }
                value={turnPassword}
                disabled={disabled}
                autoComplete="new-password"
                onChange={(e) => {
                  setTurnPassword(e.target.value);
                  edit();
                }}
              />
            </>
          )}
        </Panel>
      </SettingRow>

      <SettingRow
        label={t('settings.network.webrtc.dtls')}
        description={t('settings.network.webrtc.dtlsHint')}
      >
        <Segmented
          disabled={disabled}
          value={cipher}
          onChange={(value) => {
            setCipher(value as DTLSCipher);
            edit();
          }}
          options={[
            { label: 'ChaCha20-Poly1305', value: 'chacha20' },
            { label: 'AES-128-GCM', value: 'aes-gcm' }
          ]}
        />
      </SettingRow>

      {(changed || statusText) && (
        <div className="flex items-center justify-between gap-4">
          <span className={`text-xs ${statusColor}`}>{statusText}</span>
          <Button
            type={changed ? 'primary' : 'default'}
            icon={message ? <CheckIcon size={14} /> : undefined}
            loading={isSaving}
            disabled={disabled || !changed || blocked}
            onClick={save}
          >
            {t('settings.network.webrtc.save')}
          </Button>
        </div>
      )}
    </SettingsSection>
  );
};
