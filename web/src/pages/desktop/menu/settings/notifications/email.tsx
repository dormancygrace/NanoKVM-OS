import { useEffect, useRef, useState } from 'react';
import { Alert, Button, Input, Popconfirm, Select } from 'antd';
import { useTranslation } from 'react-i18next';

import {
  finishNotifySignIn,
  startNotifySignIn,
  type NotifyChannelUpdate,
  type NotifyProvider,
  type NotifySignIn
} from '@/api/notify.ts';
import { SettingRow } from '@/components/ui/settings.tsx';

// Product names stay as they are; "custom" is translated.
const providerNames: Record<string, string> = {
  gmail: 'Gmail · Google Workspace',
  microsoft365: 'Microsoft 365 · Exchange Online',
  outlook: 'Outlook.com · Hotmail',
  yahoo: 'Yahoo Mail',
  icloud: 'iCloud Mail',
  zoho: 'Zoho Mail',
  fastmail: 'Fastmail',
  yandex: 'Yandex Mail',
  mailru: 'Mail.ru',
  proton: 'Proton Mail'
};

// The value of the oauthToken field for a sign-in not saved yet.
const pendingPrefix = 'pending:';

// The settings a sign-in belongs to.
const clientOf = (channel: NotifyChannelUpdate) =>
  ['provider', 'oauthClientId', 'oauthTenant']
    .map((key) => (channel.settings[key] ?? '').trim())
    .join('\n');

type ProviderSelectProps = {
  id: string;
  providers: NotifyProvider[];
  settings: Record<string, string>;
  onChange: (settings: Record<string, string>) => void;
};

// The mail service of an e-mail channel: picking one fills in its server and
// the sign-in it takes first; the fields stay editable.
export const ProviderSelect = ({ id, providers, settings, onChange }: ProviderSelectProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications' });
  const current = settings.provider || 'custom';

  const pick = (value: string) => {
    const p = providers.find((item) => item.id === value);
    if (!p) return;
    const next: Record<string, string> = { ...settings, provider: value };
    if (p.host) {
      next.host = p.host;
      next.port = p.port;
      next.security = p.security;
    }
    next.auth = p.auth[0];
    if (p.tenant) next.oauthTenant = p.tenant;
    onChange(next);
  };

  return (
    <>
      <SettingRow label={t('field.provider')} htmlFor={id}>
        <Select
          id={id}
          className="w-64"
          value={current}
          options={providers.map((p) => ({
            value: p.id,
            label: providerNames[p.id] ?? t(`provider.${p.id}`)
          }))}
          onChange={pick}
        />
      </SettingRow>
      <p className="text-fg-muted m-0 text-xs">{t(`providerHelp.${current}`)}</p>
    </>
  );
};

type SignInProps = {
  channel: NotifyChannelUpdate;
  provider?: NotifyProvider;
  // A sign-in is stored and not removed on the page.
  stored: boolean;
  onSignedIn: (token: string, account: string) => void;
  onSignOut: () => void;
};

// The OAuth sign-in of an e-mail channel. Google: open its page, sign in and
// paste back the address the browser ends on (the device has no address
// Google can return to). Microsoft: enter the shown code on its page; the
// page asks the device until the sign-in is done. The result is kept when
// the settings are saved.
export const SignIn = ({ channel, provider, stored, onSignedIn, onSignOut }: SignInProps) => {
  const { t } = useTranslation('translation', { keyPrefix: 'settings.notifications.oauth' });
  const [flow, setFlow] = useState<NotifySignIn>();
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const timer = useRef<number>(undefined);
  const mounted = useRef(true);
  // The sign-in shown; an answer for a cancelled one is dropped.
  const active = useRef('');
  // The page as it is now, for a sign-in that completes later, and the
  // client the sign-in was started with.
  const latest = useRef({ channel, onSignedIn });
  useEffect(() => {
    latest.current = { channel, onSignedIn };
  });
  const startedWith = useRef('');

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      window.clearTimeout(timer.current);
    };
  }, []);

  const token = channel.settings.oauthToken ?? '';
  const account = channel.settings.oauthAccount ?? '';
  const pending = token.startsWith(pendingPrefix);

  const stop = (message = '') => {
    active.current = '';
    window.clearTimeout(timer.current);
    setFlow(undefined);
    setCode('');
    setBusy(false);
    setError(message);
  };

  const finish = async (id: string, pasted: string) => {
    try {
      const response = await finishNotifySignIn({ id, code: pasted });
      if (!mounted.current || active.current !== id) return;
      if (response.code !== 0) throw new Error(response.msg);
      const result = response.data;
      if (result.pending) {
        timer.current = window.setTimeout(() => void finish(id, ''), (result.interval ?? 5) * 1000);
        return;
      }
      // A sign-in for another service, client or tenant is not this one's.
      if (clientOf(latest.current.channel) !== startedWith.current) {
        stop(t('changed'));
        return;
      }
      stop();
      latest.current.onSignedIn(result.token ?? '', result.account ?? '');
    } catch (err) {
      if (mounted.current && active.current === id) {
        stop(t('failed', { error: err instanceof Error ? err.message : '' }));
      }
    }
  };

  const start = async () => {
    stop();
    setBusy(true);
    startedWith.current = clientOf(channel);
    try {
      const response = await startNotifySignIn(channel);
      if (!mounted.current) return;
      if (response.code !== 0) throw new Error(response.msg);
      active.current = response.data.id;
      setFlow(response.data);
      if (response.data.kind === 'device') {
        timer.current = window.setTimeout(
          () => void finish(response.data.id, ''),
          (response.data.interval ?? 5) * 1000
        );
      } else {
        setBusy(false);
      }
    } catch (err) {
      if (mounted.current) stop(t('failed', { error: err instanceof Error ? err.message : '' }));
    }
  };

  const link = (url: string) => (
    <a href={url} target="_blank" rel="noreferrer noopener">
      {url.length > 60 ? t('openPage') : url}
    </a>
  );

  return (
    <SettingRow label={t('account')} stacked>
      <div className="space-y-2">
        <p className="text-fg-muted m-0 text-xs">
          {t(provider?.oauth === 'google' ? 'clientHelpGoogle' : 'clientHelpMicrosoft')}
        </p>
        {(stored || pending) && (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm">
              {account ? t('signedIn', { account }) : t('signedInNoAccount')}
            </span>
            {pending && <span className="text-fg-muted text-xs">{t('saveToKeep')}</span>}
            <Popconfirm title={t('signOutConfirm')} onConfirm={onSignOut}>
              <Button size="small">{t('signOut')}</Button>
            </Popconfirm>
          </div>
        )}
        {!stored && !pending && !flow && (
          <p className="text-fg-muted m-0 text-sm">{t('notSignedIn')}</p>
        )}
        {!flow && (
          <Button size="small" loading={busy} onClick={() => void start()}>
            {stored || pending ? t('signInAgain') : t('signIn')}
          </Button>
        )}
        {flow?.kind === 'device' && (
          <div className="space-y-1 text-sm">
            <div>
              {t('deviceStep')} {link(flow.url)}
            </div>
            <div className="font-mono text-lg tracking-widest select-all">{flow.userCode}</div>
            <div className="text-fg-muted text-xs">{t('waiting')}</div>
            <Button size="small" onClick={() => stop()}>
              {t('cancel')}
            </Button>
          </div>
        )}
        {flow?.kind === 'code' && (
          <div className="space-y-2 text-sm">
            <div>
              {t('codeStep')} {link(flow.url)}
            </div>
            <div className="text-fg-muted text-xs">{t('codePaste')}</div>
            <Input
              aria-label={t('codeLabel')}
              placeholder="http://127.0.0.1:…/?state=…&code=…"
              value={code}
              onChange={(event) => setCode(event.target.value)}
            />
            <div className="flex gap-2">
              <Button
                size="small"
                type="primary"
                loading={busy}
                disabled={!code.trim()}
                onClick={() => {
                  setBusy(true);
                  void finish(flow.id, code);
                }}
              >
                {t('finish')}
              </Button>
              <Button size="small" onClick={() => stop()}>
                {t('cancel')}
              </Button>
            </div>
          </div>
        )}
        {error && <Alert type="error" title={error} showIcon />}
      </div>
    </SettingRow>
  );
};
