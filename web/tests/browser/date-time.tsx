import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { useAtomValue } from 'jotai';
import { createRoot } from 'react-dom/client';

import type { DateTimeConfig } from '@/lib/date-time.ts';

import '@/assets/styles/index.css';

const saved = {
  config: {
    timezone: 'UTC',
    format: '24',
    dateFormat: 'auto',
    servers: ['0.pool.ntp.org']
  } as DateTimeConfig,
  zones: ['UTC', 'Europe/Berlin'],
  now: 1793361600000,
  synchronized: false
};
let holdRead = false;
let releaseRead: (() => void) | undefined;
let completeWrite: (() => void) | undefined;
let refuse = false;
let report: (text: string) => void = () => undefined;
const adapter: AxiosAdapter = async (config) => {
  const answer = (data: unknown, msg = '', code = 0) => ({
    data: { code, data, msg },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  const snapshot = () => ({
    ...saved,
    config: { ...saved.config, servers: [...saved.config.servers] }
  });
  if (config.method === 'get') {
    const old = snapshot();
    const done = () => {
      report('GET delivered format=' + old.config.format);
      return answer(old);
    };
    if (holdRead) {
      holdRead = false;
      report('GET held');
      return new Promise((resolve) => {
        releaseRead = () => resolve(done());
      });
    }
    return done();
  }
  const clock = config.url?.endsWith('/clock');
  const body = JSON.parse(config.data);
  report(clock ? 'CLOCK requested' : 'POST ' + JSON.stringify(body));
  if (refuse)
    return answer(
      null,
      clock ? 'System clock set, but could not save it to the RTC' : 'fixture refused',
      -2
    );
  return new Promise((resolve) => {
    completeWrite = () => {
      if (clock) {
        saved.now = body.now;
        report('CLOCK received valid browser time=' + (Math.abs(Date.now() - body.now) < 60000));
      } else saved.config = body;
      report('POST committed');
      resolve(answer(snapshot()));
    };
  });
};
axios.defaults.adapter = adapter;
const { timePreferencesAtom } = await import('@/hooks/useDeviceTime.ts');
const { DateTimeSettings } = await import('@/pages/desktop/menu/settings/date-time/index.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  const preferences = useAtomValue(timePreferencesAtom);
  report = (text) => setEvents((previous) => [...previous.slice(-39), text]);
  return (
    <main style={{ padding: 24, maxWidth: 720, color: '#111', background: '#fff' }}>
      <h1>Date and time interaction fixture</h1>
      <p>Synthetic clock only; no host or device clock changes.</p>
      <nav style={{ display: 'flex', flexWrap: 'wrap', gap: 12, marginBottom: 24 }}>
        <button
          onClick={() => {
            holdRead = true;
            document.dispatchEvent(new Event('visibilitychange'));
          }}
        >
          Hold poll
        </button>
        <button
          onClick={() => {
            report('RELEASE read');
            releaseRead?.();
          }}
        >
          Release read
        </button>
        <button onClick={() => completeWrite?.()}>Complete write</button>
        <button
          onClick={() => {
            refuse = true;
          }}
        >
          Refuse writes
        </button>
        <button
          onClick={() => {
            saved.synchronized = true;
            document.dispatchEvent(new Event('visibilitychange'));
          }}
        >
          NTP synchronized
        </button>
      </nav>
      <DateTimeSettings />
      <pre data-testid="preferences">{JSON.stringify(preferences)}</pre>
      <pre data-testid="events">{events.join('\n')}</pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
