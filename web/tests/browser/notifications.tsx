import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import type {
  NotifyChannel,
  NotifyChannelUpdate,
  NotifyStatus,
  NotifyTemplate
} from '@/api/notify.ts';

import '@/assets/styles/index.css';

// Synthetic device: one Telegram channel with a stored token. A save is held
// until Complete save, then answers as the server does: ids for new
// channels, secrets left out and named in secretsSet.
const types: NotifyStatus['types'] = [
  {
    type: 'telegram',
    image: true,
    fields: [
      { key: 'token', kind: 'text', secret: true, required: true },
      { key: 'chatId', kind: 'text', required: true }
    ]
  },
  {
    type: 'email',
    image: true,
    fields: [
      {
        key: 'provider',
        kind: 'select',
        options: ['gmail', 'microsoft365', 'yahoo', 'custom'],
        default: 'custom'
      },
      { key: 'host', kind: 'text', required: true },
      { key: 'port', kind: 'number', default: '587' },
      {
        key: 'security',
        kind: 'select',
        options: ['starttls', 'tls', 'none'],
        default: 'starttls'
      },
      { key: 'auth', kind: 'select', options: ['password', 'oauth2'], default: 'password' },
      { key: 'username', kind: 'text' },
      { key: 'password', kind: 'text', secret: true, show: { auth: ['password'] } },
      { key: 'oauthClientId', kind: 'text', show: { auth: ['oauth2'] } },
      {
        key: 'oauthClientSecret',
        kind: 'text',
        secret: true,
        show: { auth: ['oauth2'], provider: ['gmail'] }
      },
      { key: 'oauthTenant', kind: 'text', show: { auth: ['oauth2'], provider: ['microsoft365'] } },
      { key: 'oauthToken', kind: 'oauth', secret: true, show: { auth: ['oauth2'] } },
      { key: 'oauthAccount', kind: 'hidden', show: { auth: ['oauth2'] } },
      { key: 'from', kind: 'email', required: true },
      { key: 'to', kind: 'text', required: true }
    ]
  }
];
// The e-mail sign-ins: Google pastes an address; Microsoft stays pending
// until Complete sign-in.
const providers: NotifyStatus['providers'] = [
  {
    id: 'gmail',
    host: 'smtp.gmail.com',
    port: '587',
    security: 'starttls',
    auth: ['oauth2', 'password'],
    oauth: 'google'
  },
  {
    id: 'microsoft365',
    host: 'smtp.office365.com',
    port: '587',
    security: 'starttls',
    auth: ['oauth2', 'password'],
    oauth: 'microsoft',
    tenant: 'organizations'
  },
  { id: 'yahoo', host: 'smtp.mail.yahoo.com', port: '465', security: 'tls', auth: ['password'] },
  { id: 'custom', host: '', port: '', security: '', auth: ['password'] }
];
let signInDone = false;
let stored: NotifyChannel[] = [
  {
    id: 'c1',
    type: 'telegram',
    name: 'Before',
    enabled: true,
    events: ['power'],
    settings: { chatId: '1' },
    secretsSet: ['token']
  }
];
let storedLanguage = 'en';
let storedTemplates: NotifyTemplate[] = [];
let nextId = 2;
let completeSave: (() => void) | undefined;
let report: (text: string) => void = () => undefined;

const status = (): NotifyStatus => ({
  language: storedLanguage,
  languages: ['en', 'ru'],
  channels: stored.map((channel) => ({ ...channel, settings: { ...channel.settings } })),
  templates: storedTemplates,
  messages: [
    { key: 'power.off', kind: 'power', vars: null, title: 'Host powered off', body: 'Off.' }
  ],
  commonVars: ['device', 'time', 'details'],
  types,
  events: ['power', 'hdmi', 'security'],
  providers
});

const adapter: AxiosAdapter = async (config) => {
  const answer = (data: unknown) => ({
    data: { code: 0, data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  if (config.method === 'get') return answer(status());
  if (config.url?.endsWith('/preview')) {
    // The server's preview: the template's title, or the standard one.
    const { language, template } = JSON.parse(config.data) as {
      language: string;
      template: NotifyTemplate | null;
    };
    const own = template?.messages['power.off'];
    const title = own?.title || 'Host powered off';
    report(`PREVIEW language=${language} template=${template?.name ?? 'standard'}`);
    return answer([{ key: 'power.off', title, body: 'Off.', text: 'Off.\n\nkvm' }]);
  }
  if (config.url?.endsWith('/oauth/start')) {
    const channel = JSON.parse(config.data) as NotifyChannelUpdate;
    report(`SIGN-IN start ${channel.settings.provider}`);
    signInDone = false;
    return answer(
      channel.settings.provider === 'gmail'
        ? { id: 'f1', kind: 'code', url: 'https://accounts.example/auth?long=1', expiresIn: 900 }
        : {
            id: 'f1',
            kind: 'device',
            url: 'https://microsoft.example/devicelogin',
            userCode: 'ABCD-1234',
            interval: 1,
            expiresIn: 900
          }
    );
  }
  if (config.url?.endsWith('/oauth/finish')) {
    const { code } = JSON.parse(config.data) as { code: string };
    report(`SIGN-IN finish code=${code}`);
    if (!code && !signInDone) return answer({ pending: true, interval: 1 });
    return answer({ pending: false, token: 'pending:h1', account: 'me@example.com' });
  }
  const sent = JSON.parse(config.data) as {
    language: string;
    channels: NotifyChannelUpdate[];
    templates: NotifyTemplate[];
  };
  report(
    `PUT held language=${sent.language} ` +
      sent.channels.map((channel) => `${channel.id || 'new'}:${channel.name}`).join(',')
  );
  report(
    'TEMPLATES ' +
      sent.templates
        .map((template) => `${template.name}=${JSON.stringify(template.messages)}`)
        .join(';') +
      ' USED ' +
      sent.channels
        .map((channel) => {
          const used = sent.templates.find((template) => template.id === channel.template);
          return used ? used.name : 'standard';
        })
        .join(',')
  );
  return new Promise((resolve) => {
    completeSave = () => {
      completeSave = undefined;
      stored = sent.channels.map((channel) => {
        const old = stored.find((item) => item.id === channel.id);
        const token = channel.settings.token || (old?.secretsSet.includes('token') ? 'kept' : '');
        const settings = { ...channel.settings };
        delete settings.token;
        return {
          id: channel.id || `c${nextId++}`,
          type: channel.type,
          name: channel.name,
          enabled: channel.enabled,
          events: channel.events,
          settings,
          secretsSet: token && !channel.clear.includes('token') ? ['token'] : [],
          template: channel.template
        };
      });
      storedTemplates = sent.templates;
      storedLanguage = sent.language;
      report('PUT committed');
      resolve(answer(status()));
    };
  });
};
axios.defaults.adapter = adapter;
const { Notifications } = await import('@/pages/desktop/menu/settings/notifications/index.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  report = (text) => setEvents((previous) => [...previous.slice(-39), text]);
  return (
    <main style={{ padding: 24, maxWidth: 720, color: '#111', background: '#fff' }}>
      <h1>Notifications interaction fixture</h1>
      <p>Synthetic local data only; no device requests.</p>
      <nav style={{ display: 'flex', gap: 12, marginBottom: 24 }}>
        <button onClick={() => completeSave?.()}>Complete save</button>
        <button onClick={() => (signInDone = true)}>Complete sign-in</button>
      </nav>
      <Notifications />
      <pre data-testid="events">{events.join('\n')}</pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
