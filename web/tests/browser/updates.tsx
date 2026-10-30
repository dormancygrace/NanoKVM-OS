import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import type { Components, UpdateNotice } from '@/api/updates.ts';

import '@/assets/styles/index.css';

const saved = {
  installed: { version: '3.6.0' },
  apk: { state: 'idle', upgrades: '', message: '', log: '', reboot: false, kernel: '7.3' },
  notice: { enabled: true, state: 'ok', checkedAt: 1793361600, updates: [] } as UpdateNotice,
  components: {
    packages: [{ name: 'nanokvm-app', installed: '3.6.0-r0' }],
    alpine: '3.24',
    kernel: '7.3',
    bootKernel: '7.3'
  } as Components,
  channel: { channel: 'stable', available: true, rollbackAvailable: false }
};
let report: (event: string) => void = () => undefined;
const adapter: AxiosAdapter = async (config) => {
  const answer = () => ({
    data: { code: 0, msg: '', data: structuredClone(saved) },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  if (config.method === 'get') return answer();
  const body = config.data ? JSON.parse(config.data) : {};
  const action = config.url?.split('/').pop();
  report(config.method + ' ' + action + ' ' + JSON.stringify(body));
  if (action === 'check') saved.apk.state = 'checking';
  else if (action === 'install' || action === 'rollback') saved.apk.state = 'installing';
  else if (action === 'reboot') saved.apk.state = 'rebooting';
  else if (action === 'channel') {
    saved.channel.channel = body.channel;
    saved.apk.state = 'idle';
    saved.apk.upgrades = '';
    saved.notice.state = 'never';
    saved.notice.updates = [];
    saved.components.packages.forEach((p) => delete p.available);
  } else if (action === 'notice') saved.notice.enabled = body.enabled;
  else throw new Error('Unexpected updates fixture request: ' + config.url);
  return answer();
};
axios.defaults.adapter = adapter;
const { Updates } = await import('@/pages/desktop/menu/settings/updates/index.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  report = (event) => setEvents((previous) => [...previous, event]);
  function refresh() {
    document.dispatchEvent(new Event('visibilitychange'));
  }
  return (
    <main style={{ padding: 24, maxWidth: 760, color: '#111', background: '#fff' }}>
      <h1>Updates interaction fixture</h1>
      <p>Synthetic repositories and operations only. Nothing is installed or rebooted.</p>
      <nav style={{ display: 'flex', flexWrap: 'wrap', gap: 12, marginBottom: 24 }}>
        <button
          onClick={() => {
            saved.notice.updates = [
              { name: 'nanokvm-app', installed: '3.6.0-r0', available: '3.6.1-r0' }
            ];
            saved.components.packages[0].available = '3.6.1-r0';
            saved.notice.state = 'ok';
            refresh();
          }}
        >
          Offer core update
        </button>
        <button
          onClick={() => {
            saved.apk.state = 'ready';
            saved.apk.upgrades = 'busybox-1.37.0-r0 < 1.37.0-r1';
            refresh();
          }}
        >
          Complete package check
        </button>
        <button
          onClick={() => {
            saved.apk = {
              ...saved.apk,
              state: 'installed',
              upgrades: '',
              reboot: true,
              log: 'Synthetic transaction completed'
            };
            saved.notice.state = 'ok';
            saved.notice.updates = [];
            saved.installed.version = '3.6.1';
            saved.components.packages = [{ name: 'nanokvm-app', installed: '3.6.1-r0' }];
            refresh();
          }}
        >
          Complete installation
        </button>
        <button
          onClick={() => {
            saved.apk.state = 'failed';
            saved.apk.message = 'Fixture APK index failed';
            saved.notice.state = 'failed';
            saved.notice.message = 'Fixture repository failed';
            refresh();
          }}
        >
          Fail operation
        </button>
        <button
          onClick={() => {
            saved.channel = { channel: 'stable', available: true, rollbackAvailable: true };
            saved.installed.version = '3.7.0-beta.1';
            refresh();
          }}
        >
          Offer stable rollback
        </button>
      </nav>
      <Updates />
      <pre data-testid="events">{events.join('\n')}</pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
