import { useState } from 'react';
import { AuthContext } from '@/contexts/auth';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { getDefaultStore, useAtomValue } from 'jotai';
import { createRoot } from 'react-dom/client';

import { brandingAtom, DEFAULT_BUTTON_COLOR } from '@/jotai/branding';
import { menuDisabledItemsAtom } from '@/jotai/settings';
import { AppTheme } from '@/components/app-theme';

import '@/assets/styles/index.css';

const initial = {
  logoRevision: '',
  faviconRevision: '',
  customLogoAvailable: false,
  customFaviconAvailable: false,
  buttonColor: '#112233',
  customButtonColor: true
};
const saved = { ...initial };
let title = 'Fixture device';
let report: (text: string) => void = () => undefined;
let hold = false;
let refuse = false;
const completions: (() => void)[] = [];
const adapter: AxiosAdapter = async (config) => {
  const answer = (data: unknown, code = 0) => ({
    data: { code, data, msg: code ? 'fixture refused' : '' },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  if (config.method === 'get') return answer({ title });
  const body = config.data ? JSON.parse(config.data) : {};
  report(config.method?.toUpperCase() + ' ' + config.url + ' ' + JSON.stringify(body));
  const commit = () => {
    if (refuse) {
      report('REFUSED');
      return answer(null, -2);
    }
    if (config.url?.endsWith('button-color')) {
      saved.buttonColor = config.method === 'delete' ? DEFAULT_BUTTON_COLOR : body.color;
      saved.customButtonColor = config.method !== 'delete';
    } else if (config.url?.endsWith('web-title')) title = body.title;
    report('COMMITTED');
    return answer({ ...saved });
  };
  if (hold) return new Promise((resolve) => completions.push(() => resolve(commit())));
  return commit();
};
axios.defaults.adapter = adapter;
getDefaultStore().set(brandingAtom, initial);
getDefaultStore().set(menuDisabledItemsAtom, ['keyboard', 'terminal']);
const { Appearance } = await import('@/pages/desktop/menu/settings/appearance');
await i18nReady;
await i18next.changeLanguage('en');
export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  const [role, setRole] = useState<'admin' | 'user'>('admin');
  report = (text) => setEvents((old) => [...old.slice(-39), text]);
  const state = useAtomValue(brandingAtom);
  const menu = useAtomValue(menuDisabledItemsAtom);
  return (
    <AuthContext.Provider value={{ account: { username: 'fixture', role } }}>
      <AppTheme>
        <main style={{ padding: 24, maxWidth: 720 }}>
          <h1>Appearance interaction fixture</h1>
          <p>Synthetic responses only; no device requests.</p>
          <nav style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 24 }}>
            <button
              onClick={() => {
                hold = true;
                report('HOLD writes');
              }}
            >
              Hold writes
            </button>
            <button onClick={() => completions.shift()?.()}>Complete write</button>
            <button
              onClick={() => {
                refuse = true;
                report('REFUSE writes');
              }}
            >
              Refuse writes
            </button>
            <button onClick={() => setRole('user')}>Use regular user</button>
            <button onClick={() => setRole('admin')}>Use administrator</button>
          </nav>
          <Appearance />
          <pre data-testid="branding" style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
            {JSON.stringify(state)}
          </pre>
          <pre data-testid="menu">{JSON.stringify(menu)}</pre>
          <pre data-testid="events" style={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
            {events.join('\n')}
          </pre>
        </main>
      </AppTheme>
    </AuthContext.Provider>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
