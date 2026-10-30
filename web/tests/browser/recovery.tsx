import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import '@/assets/styles/index.css';

const query = new URLSearchParams(location.search);
const admin = 'fixture-admin-only';
const archive = 'fixture-archive-only';
const backup = 'fixture-export-only';
let report: (event: string) => void = () => undefined;
let preview: Record<string, unknown> | undefined;
let failed = false;
const adapter: AxiosAdapter = async (config) => {
  const answer = (data: unknown, code = 0, msg = '') => ({
    data: { code, data, msg },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  });
  const action = config.url?.split('/').pop();
  if (config.method === 'get')
    return answer({
      available: !query.has('unavailable'),
      status: { state: query.has('pending') ? 'pending' : 'idle' }
    });
  const body = JSON.parse(config.data);
  report(
    action + ' admin=' + (body.password === admin) + ' sensitive=' + (config.sensitive === true)
  );
  if (body.password !== admin) return answer(null, -1, 'Wrong administrator password');
  if (query.get('failure') === action && !failed) {
    failed = true;
    return answer(null, -1, 'Fixture ' + action + ' refused');
  }
  if (action === 'export') {
    report('export password=' + (body.archivePassword === backup));
    return answer({ archive: btoa('synthetic encrypted archive') });
  }
  if (action === 'preview') {
    preview = body;
    report(
      'preview ' +
        JSON.stringify({
          ...body.options,
          archivePassword: body.archivePassword === archive,
          archive: body.archive === btoa('fixture archive bytes')
        })
    );
    return answer({
      token: 'fixture-token',
      version: '3.6.0',
      changed: ['settings/example'],
      skipped: ['network/preserved'],
      removePackages: [],
      packages: ['demo']
    });
  }
  if (action === 'apply') {
    const { token, ...request } = body;
    report(
      'apply confirmed snapshot=' +
        (token === 'fixture-token' && JSON.stringify(request) === JSON.stringify(preview))
    );
    return answer(null);
  }
  if (action === 'reboot') return answer(null);
  throw new Error('Unexpected recovery fixture request: ' + config.url);
};
axios.defaults.adapter = adapter;
// Verify the download action without creating a file in the user's Downloads.
HTMLAnchorElement.prototype.click = function () {
  report('download name=' + this.download + ' blob=' + this.href.startsWith('blob:'));
};
const { Recovery } = await import('@/pages/desktop/menu/settings/recovery/index.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [events, setEvents] = useState<string[]>([]);
  report = (event) => setEvents((previous) => [...previous, event]);
  function selectArchive(oversized = false) {
    const input = document.querySelector<HTMLInputElement>('input[type=file]')!;
    const transfer = new DataTransfer();
    transfer.items.add(
      new File(
        [oversized ? new Uint8Array(4 * 1024 * 1024 + 1) : 'fixture archive bytes'],
        'fixture.nkos'
      )
    );
    input.files = transfer.files;
    input.dispatchEvent(new Event('change', { bubbles: true }));
  }
  return (
    <main style={{ padding: 24, maxWidth: 760, color: '#111', background: '#fff' }}>
      <h1>Recovery interaction fixture</h1>
      <p>
        All requests are synthetic. Passwords: fixture-admin-only / fixture-archive-only /
        fixture-export-only.
      </p>
      <button onClick={() => selectArchive()}>Select fixture archive</button>
      <button onClick={() => selectArchive(true)}>Select oversized archive</button>
      <Recovery />
      <pre data-testid="events">{events.join('\n')}</pre>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
