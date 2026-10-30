import { useState } from 'react';
import { i18nReady } from '@/i18n';
import axios from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import '@/assets/styles/index.css';

let scenario = 'Healthy';
const scenarios = ['Healthy', 'APK failure', 'EDID pending', 'Multiple problems'];
const content = [
  'wrapped prefix '.repeat(180),
  'needle first',
  'wrapped middle '.repeat(180),
  'needle second',
  ...Array.from({ length: 30 }, (_, i) => 'tail ' + i)
].join('\n');

// Match the diagnostics endpoint's normalized states, not APK's internal status.
// All calls are synthetic; this fixture never connects to a device.
axios.defaults.adapter = async (config) => {
  const snapshot = {
    collectedAt: 1803900000000,
    versions: {
      application: '3.6.0',
      image: '1.8',
      alpine: '3.24',
      kernel: 'fixture',
      modules: { state: 'ok' },
      boot: { state: 'ok' },
      packages: [{ name: 'fixture-package', version: '1.0-r0', state: 'installed' }]
    },
    services: [
      { name: 'NanoKVM', state: 'running', optional: false },
      { name: 'optional-vpn', state: 'stopped', optional: true }
    ],
    video: { enabled: true, signal: false, state: 'no-signal', edidState: 'saved-profile' },
    usb: { selected: [] as string[], binding: { state: 'unbound', detail: 'No gadget selected' } },
    apk: { state: 'idle', errorCategory: '' },
    firewall: { state: 'ok', hookChains: [] }
  };
  if (scenario === 'APK failure' || scenario === 'Multiple problems')
    snapshot.apk = { state: 'error', errorCategory: 'operation-failed' };
  if (scenario === 'EDID pending' || scenario === 'Multiple problems')
    snapshot.video.edidState = 'power-cycle-pending';
  if (scenario === 'Multiple problems') {
    snapshot.versions.modules.state = 'error';
    snapshot.versions.boot.state = 'pending-reboot';
    snapshot.usb.selected = ['keyboard'];
    snapshot.usb.binding.state = 'error';
    snapshot.firewall.state = 'error';
    snapshot.services[0].state = 'stopped';
    snapshot.services[1].state = 'error';
  }
  let data: unknown;
  if (config.url === '/api/vm/diagnostics') data = snapshot;
  else if (config.url === '/api/vm/logs/boots')
    data = { boots: [{ id: 'current', startedAt: 0, savedAt: 0 }], archiveAvailable: true };
  else if (config.url === '/api/vm/logs')
    data = {
      source: 'system',
      boot: 'current',
      archiveAvailable: true,
      state: 'ready',
      content,
      lines: content.split('\n').length,
      truncated: false,
      collectedAt: 1803900000000
    };
  else throw new Error('Unexpected fixture request: ' + config.url);
  return { data: { code: 0, data }, status: 200, statusText: 'OK', headers: {}, config };
};
const { Diagnostics } = await import('@/pages/desktop/menu/settings/system/diagnostics.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [selected, setSelected] = useState(scenario);
  return (
    <main style={{ padding: 24, maxWidth: 720 }}>
      <h1>Diagnostics review fixture</h1>
      <nav aria-label="Fixture scenarios" style={{ display: 'flex', gap: 12, marginBottom: 16 }}>
        {scenarios.map((name) => (
          <button
            key={name}
            onClick={() => {
              scenario = name;
              setSelected(name);
            }}
          >
            {name}
          </button>
        ))}
      </nav>
      <Diagnostics key={selected} />
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
