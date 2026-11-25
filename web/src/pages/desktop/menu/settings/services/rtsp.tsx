import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Input, InputNumber, Popconfirm, Switch } from 'antd';
import { CopyIcon, EyeIcon, EyeOffIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import { getRtsp, setRtsp, type RtspSettings } from '@/api/stream.ts';
import { writeClipboardText } from '@/lib/clipboard.ts';
import { pollWhileVisible } from '@/lib/visible-poll.ts';
import {
  IconButton,
  Panel,
  SettingRow,
  SettingsSection,
  StatusBadge
} from '@/components/ui/settings.tsx';

// Services > RTSP: the device stream for OBS, VLC and other players that open a
// link. The link carries the user name and the generated password.
export const RtspPage = () => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.netServices.rtsp' });
  const [settings, setSettings] = useState<RtspSettings>();
  const [port, setPort] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [shown, setShown] = useState(false);
  const [copied, setCopied] = useState(false);

  const read = useCallback(() => {
    getRtsp()
      .then((rsp) => {
        if (rsp.code !== 0) return setError(rsp.msg);
        setError('');
        setSettings(rsp.data);
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)));
  }, []);

  useEffect(() => {
    read();
    return pollWhileVisible(read, 5000);
  }, [read]);

  useEffect(() => {
    if (settings) setPort(settings.port);
  }, [settings?.port]); // eslint-disable-line react-hooks/exhaustive-deps

  const save = (change: Parameters<typeof setRtsp>[0]) => {
    setSaving(true);
    setRtsp(change)
      .then((rsp) => {
        if (rsp.code !== 0) return setError(rsp.msg);
        setError('');
        setSettings(rsp.data);
      })
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)))
      .finally(() => setSaving(false));
  };

  if (!settings) return error ? <Alert type="error" showIcon message={error} /> : null;

  // An IPv6 literal comes with its brackets.
  const host = window.location.hostname;
  const link = (password: string) =>
    `rtsp://${settings.username}:${password}@${host}:${settings.port}${settings.path}`;
  const copy = () => {
    void writeClipboardText(link(settings.password)).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    });
  };
  const portChanged = port !== null && port !== settings.port;

  return (
    <div className="space-y-6">
      <SettingsSection description={t('description')}>
        <SettingRow label={t('enable')} description={t('enableHint')} htmlFor="rtsp-enable">
          <Switch
            id="rtsp-enable"
            checked={settings.enabled}
            loading={saving}
            onChange={(enabled) => save({ enabled })}
          />
        </SettingRow>
        {settings.enabled && (
          <SettingRow label={t('port')} description={t('portHint')} htmlFor="rtsp-port">
            <div className="flex items-center gap-2">
              <InputNumber
                id="rtsp-port"
                min={1}
                max={65535}
                precision={0}
                value={port}
                onChange={(value) => setPort(value)}
                onPressEnter={() => portChanged && port && save({ port })}
              />
              {portChanged && (
                <Button size="small" loading={saving} onClick={() => port && save({ port })}>
                  {t('save')}
                </Button>
              )}
            </div>
          </SettingRow>
        )}
      </SettingsSection>

      {error && <Alert type="error" showIcon message={error} />}
      {settings.enabled && settings.error && (
        <Alert type="error" showIcon message={settings.error} />
      )}

      {settings.enabled && settings.password && (
        <SettingsSection
          title={t('link')}
          actions={
            settings.running ? (
              <StatusBadge tone={settings.streaming ? 'success' : 'neutral'}>
                {t(settings.streaming ? 'streaming' : 'waiting')}
              </StatusBadge>
            ) : undefined
          }
        >
          <Panel className="space-y-3">
            <div className="flex items-center gap-1">
              <Input
                readOnly
                aria-label={t('link')}
                value={link(shown ? settings.password : '••••••••')}
                className="font-mono"
              />
              <IconButton
                label={t(shown ? 'hide' : 'show')}
                icon={shown ? <EyeOffIcon size={16} /> : <EyeIcon size={16} />}
                onClick={() => setShown(!shown)}
              />
              <IconButton
                label={t(copied ? 'copied' : 'copy')}
                icon={<CopyIcon size={16} />}
                onClick={copy}
              />
            </div>
            <p className="text-fg-muted m-0 text-xs">{t('linkHint')}</p>
            <Popconfirm
              title={t('newPasswordConfirm')}
              okText={t('newPassword')}
              onConfirm={() => save({ newPassword: true })}
            >
              <Button size="small" loading={saving}>
                {t('newPassword')}
              </Button>
            </Popconfirm>
          </Panel>
        </SettingsSection>
      )}
    </div>
  );
};
