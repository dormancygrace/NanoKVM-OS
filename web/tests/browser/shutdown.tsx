import i18n, { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import { createRoot } from 'react-dom/client';

import '@/assets/styles/index.css';

const events: unknown[] = [];
// Install before importing http.ts, which creates its own axios instance.
// Every request is inert, including explicit destructive confirmation.
const adapter: AxiosAdapter = async (config) => {
  events.push({ method: config.method, url: config.url, body: JSON.parse(config.data) });
  document.querySelector('[data-testid="events"]')!.textContent = JSON.stringify(events);
  return {
    config,
    status: 200,
    statusText: 'OK',
    headers: {},
    data: { code: -1, msg: 'Fixture refused preparation', data: null }
  };
};
axios.defaults.adapter = adapter;
await i18nReady;
await i18n.changeLanguage('en');
const { Shutdown } = await import('@/pages/desktop/menu/settings/system/shutdown');
createRoot(document.getElementById('root')!).render(
  <main style={{ padding: 24 }}>
    <Shutdown />
    <pre data-testid="events">[]</pre>
  </main>
);
