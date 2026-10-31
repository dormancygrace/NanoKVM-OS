import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import type { NotifyChannel, NotifyChannelUpdate, NotifyStatus } from '@/api/notify.ts';

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
  }
];
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
let nextId = 2;
let completeSave: (() => void) | undefined;
let report: (text: string) => void = () => undefined;

const status = (): NotifyStatus => ({
  language: storedLanguage,
  languages: ['en', 'ru'],
  channels: stored.map((channel) => ({ ...channel, settings: { ...channel.settings } })),
  types,
  events: ['power', 'hdmi', 'security']
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
  const sent = JSON.parse(config.data) as { language: string; channels: NotifyChannelUpdate[] };
  report(
    `PUT held language=${sent.language} ` +
      sent.channels.map((channel) => `${channel.id || 'new'}:${channel.name}`).join(',')
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
          secretsSet: token && !channel.clear.includes('token') ? ['token'] : []
        };
      });
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
      </nav>
      <Notifications />
      <pre data-testid="events">{events.join('\n')}</pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
