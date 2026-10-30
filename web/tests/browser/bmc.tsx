import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { AxiosError, isAxiosError, type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import type { BmcSettings, BmcStatus } from '@/api/bmc.ts';

import '@/assets/styles/index.css';

const saved: BmcStatus = {
  redfish: true,
  ipmi: false,
  ipmiInstalled: true,
  ipmiRunning: false,
  username: 'initial',
  passwordSet: !location.search.includes('no-password'),
  ipmiPort: 623,
  holdSeconds: 5,
  powerLedConnected: true
};
if (!saved.passwordSet) saved.redfish = false;
let completeWrite: (() => void) | undefined;
let releaseRead: (() => void) | undefined;
let holdRead = false;
let failWrite = false;
let report: (text: string) => void = () => undefined;
const originalLog = console.log;
console.log = (...args: unknown[]) => {
  if (
    args.some(
      (value) => isAxiosError(value) && String(value.config?.data).includes('fixture-secret')
    )
  )
    report('PASSWORD LOGGED');
  else originalLog(...args);
};
const adapter: AxiosAdapter = async (config) => {
  const answer = (data: unknown) => ({
    data: { code: 0, data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  if (config.method === 'get') {
    const snapshot = { ...saved };
    const done = () => {
      report(
        `GET delivered user=${snapshot.username} redfish=${snapshot.redfish} ipmi=${snapshot.ipmi}`
      );
      return answer(snapshot);
    };
    if (holdRead) {
      holdRead = false;
      report('GET held');
      return new Promise((resolve) => {
        releaseRead = () => resolve(done());
      });
    }
    await new Promise((resolve) => setTimeout(resolve, 40));
    return done();
  }
  const { password, ...settings } = JSON.parse(config.data) as BmcSettings;
  report(`PUT held user=${settings.username} redfish=${settings.redfish} ipmi=${settings.ipmi}`);
  if (failWrite) {
    report('PUT refused');
    throw new AxiosError('fixture refused', 'ERR_BAD_RESPONSE', config, undefined, {
      ...answer(null),
      status: 500
    });
  }
  return new Promise((resolve) => {
    completeWrite = () => {
      Object.assign(saved, settings, { passwordSet: saved.passwordSet || !!password });
      report('PUT committed');
      resolve(answer(null));
    };
  });
};
axios.defaults.adapter = adapter;
const { BmcSettingsPage } = await import('@/pages/desktop/menu/settings/system/bmc.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  report = (text) => setEvents((previous) => [...previous.slice(-39), text]);
  return (
    <main style={{ padding: 24, maxWidth: 720, color: '#111', background: '#fff' }}>
      <h1>BMC interaction fixture</h1>
      <p>Synthetic local data only; no device requests.</p>
      <nav style={{ display: 'flex', gap: 12, marginBottom: 24 }}>
        <button onClick={() => completeWrite?.()}>Complete write</button>
        <button
          onClick={() => {
            holdRead = true;
          }}
        >
          Hold next read
        </button>
        <button
          onClick={() => {
            report('RELEASE read');
            releaseRead?.();
          }}
        >
          Release read
        </button>
        <button
          onClick={() => {
            failWrite = true;
          }}
        >
          Fail writes
        </button>
      </nav>
      <BmcSettingsPage />
      <pre data-testid="events">{events.join('\n')}</pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
