import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import '@/assets/styles/index.css';

let saved = { enabled: true, time: '03:00' };
let completeWrite: (() => void) | undefined;
let refuse = false;
let report: (text: string) => void = () => undefined;
const adapter: AxiosAdapter = async (config) => {
  const answer = (data: unknown, code = 0, msg = '') => ({
    data: { code, data, msg },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  if (config.method === 'get') return answer({ schedule: saved, timezone: 'UTC' });
  const next = JSON.parse(config.data) as typeof saved;
  report('PUT ' + JSON.stringify(next));
  return new Promise((resolve) => {
    completeWrite = () => {
      completeWrite = undefined;
      if (refuse) {
        report('REFUSED');
        resolve(answer(null, -1, 'Synthetic refusal'));
      } else {
        saved = next;
        report('COMMITTED ' + JSON.stringify(saved));
        resolve(answer(null));
      }
    };
  });
};
axios.defaults.adapter = adapter;
const { IndexSchedule } = await import('@/pages/desktop/menu/settings/software/index-schedule');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  report = (text) => setEvents((previous) => [...previous, text]);
  return (
    <main style={{ padding: 24, maxWidth: 720, color: '#111', background: '#fff' }}>
      <h1>Package index schedule fixture</h1>
      <p>Synthetic local data only; no package manager or device requests.</p>
      <button onClick={() => completeWrite?.()}>Complete write</button>
      <button
        onClick={() => {
          refuse = true;
        }}
      >
        Refuse writes
      </button>
      <IndexSchedule />
      <button>Outside schedule</button>
      <pre data-testid="events">{events.join('\n')}</pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
