import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { AxiosError, isAxiosError, type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import type { AddonManifest } from '@/api/addons.ts';

import '@/assets/styles/index.css';

// Exercise the production component and Http interceptors with controllable
// response order. The adapter never sends requests to a device or the network.
let saved = 'initial';
let finishPost: (() => void) | undefined;
let finishOld: (() => void) | undefined;
let failPost = false;
let holdNextRead = false;
let report: (text: string) => void = () => undefined;
const originalLog = console.log;
console.log = (...args: unknown[]) => {
  const leaked = args.some(
    (value) =>
      isAxiosError(value) &&
      typeof value.config?.data === 'string' &&
      value.config.data.includes('fixture-secret')
  );
  // Record the leak without putting even the synthetic credential in logs.
  if (leaked) report('PASSWORD LOGGED');
  else originalLog(...args);
};
const adapter: AxiosAdapter = async (config) => {
  const answer = (data: Record<string, unknown>) => ({
    data: { code: 0, data },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  const source = config.url!.split('/').pop()!;
  if (config.method === 'get') {
    const name = source === 'new' ? 'CURRENT NEW' : source === 'old' ? 'STALE OLD' : saved;
    const done = () => {
      report(`GET ${source} delivered name=${name}`);
      return answer({ name, state: source === 'old' ? 'old' : 'ready' });
    };
    if (source === 'old' || holdNextRead) {
      holdNextRead = false;
      report(`GET ${source} held name=${name}`);
      return new Promise((resolve) => {
        finishOld = () => resolve(done());
      });
    }
    // Preserve asynchronous network ordering, including POST -> refreshed GET.
    await new Promise((resolve) => setTimeout(resolve, 40));
    return done();
  }
  if (failPost) {
    report('POST failed HTTP500');
    throw new AxiosError('fixture rejected', 'ERR_BAD_RESPONSE', config, undefined, {
      ...answer({}),
      status: 500
    });
  }
  const body = JSON.parse(config.data || '{}') as { name?: string };
  report('POST held name=' + body.name);
  return new Promise((resolve) => {
    finishPost = () => {
      saved = body.name ?? saved;
      report('POST committed name=' + saved);
      resolve(answer({}));
    };
  });
};
// Set the adapter before Http constructs its axios instance, without reaching
// into private component or client state.
axios.defaults.adapter = adapter;
const { AddonPage } = await import('@/pages/desktop/menu/settings/addon/index.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  const [source, setSource] = useState('settings');
  report = (text) => setEvents((previous) => [...previous.slice(-49), text]);
  const manifest: AddonManifest = {
    id: 'demo',
    api: 1,
    title: { en: 'Review fixture' },
    backend: { socket: 'demo.sock' },
    settings: {
      group: 'system',
      form: {
        source: '/api/addon/demo/' + source,
        submit: '/api/addon/demo/settings',
        refresh: 2,
        fields: [
          { key: 'name', type: 'text', label: { en: 'Name' } },
          { key: 'password', type: 'password', label: { en: 'Synthetic secret' } },
          { key: 'state', type: 'status', label: { en: 'Service state' } }
        ]
      }
    }
  };
  return (
    <main style={{ padding: 24, maxWidth: 720, color: '#111', background: '#fff' }}>
      <h1>Add-on form interaction fixture</h1>
      <p>Only synthetic local data; requests use a controlled test adapter.</p>
      <nav style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginBottom: 24 }}>
        <button onClick={() => finishPost?.()}>Complete save</button>
        <button
          onClick={() => {
            holdNextRead = true;
          }}
        >
          Hold next poll
        </button>
        <button onClick={() => setSource('old')}>Hold old source</button>
        <button onClick={() => setSource('new')}>Switch source</button>
        <button
          onClick={() => {
            report('RELEASE old read');
            finishOld?.();
          }}
        >
          Release old read
        </button>
        <button
          onClick={() => {
            failPost = true;
          }}
        >
          Fail saves
        </button>
      </nav>
      <AddonPage manifest={manifest} />
      <pre data-testid="events" aria-label="Test events">
        {events.join('\n')}
      </pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
