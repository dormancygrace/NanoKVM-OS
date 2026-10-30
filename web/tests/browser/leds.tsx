import { useEffect, useState } from 'react';
import { i18nReady } from '@/i18n';
import axios, { type AxiosAdapter } from 'axios';
import i18next from 'i18next';
import { createRoot } from 'react-dom/client';

import type { LEDSource, LEDStatus } from '@/lib/led-stream.ts';

import '@/assets/styles/index.css';

const sources = new Set<Source>();
const currentSource = () => sources.values().next().value;
let connections = 0;
let closed = 0;
let polls = 0;
let level = { pwr: true, hdd: false };
class Source implements LEDSource {
  onmessage: LEDSource['onmessage'] = null;
  onerror: LEDSource['onerror'] = null;
  constructor() {
    sources.add(this);
    connections++;
    queueMicrotask(() => this.send(level));
  }
  send(status: LEDStatus) {
    this.onmessage?.call(
      this as unknown as EventSource,
      new MessageEvent('message', { data: JSON.stringify(status) })
    );
  }
  close() {
    closed++;
    sources.delete(this);
  }
  fail() {
    this.onerror?.call(this as unknown as EventSource, new Event('error'));
  }
}
// This fixture never connects to a device or sends a power action.
window.EventSource = Source as unknown as typeof EventSource;
const adapter: AxiosAdapter = async (config) => {
  if (config.method !== 'get') throw new Error('Power actions are disabled in this fixture');
  polls++;
  return {
    data: { code: 0, data: { gpio: level } },
    status: 200,
    statusText: 'OK',
    headers: {},
    config
  };
};
axios.defaults.adapter = adapter;
const { Power } = await import('@/pages/desktop/menu/power/index.tsx');
await i18nReady;
await i18next.changeLanguage('en');

export function Fixture() {
  const [mounted, setMounted] = useState(true);
  const [stats, setStats] = useState('');
  const [flash, setFlash] = useState('No flash measured');
  useEffect(() => {
    const timer = setInterval(
      () => setStats(`connections=${connections} closed=${closed} polls=${polls}`),
      100
    );
    return () => clearInterval(timer);
  }, []);
  useEffect(() => {
    let active = false;
    let started = 0;
    let frames = 0;
    let frame = 0;
    const countFrame = () => {
      frames++;
      frame = requestAnimationFrame(countFrame);
    };
    const observe = () => {
      const led = document.querySelector('[aria-label="HDD LED"]');
      const on = led?.classList.contains('text-amber-400') ?? false;
      if (on === active) return;
      active = on;
      if (on) {
        started = performance.now();
        frames = 0;
        frame = requestAnimationFrame(countFrame);
      } else {
        cancelAnimationFrame(frame);
        setFlash(
          `Last flash: ${(performance.now() - started).toFixed(1)} ms, ${frames} display frames`
        );
      }
    };
    const observer = new MutationObserver(observe);
    observer.observe(document.getElementById('root')!, {
      attributes: true,
      attributeFilter: ['class'],
      childList: true,
      subtree: true
    });
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, []);
  const send = (hdd: boolean) => {
    level = { ...level, hdd };
    currentSource()?.send(level);
  };
  return (
    <main style={{ padding: 24, color: '#ddd', background: '#171717', minHeight: '100vh' }}>
      <h1>HDD LED timing</h1>
      <p>Synthetic events only. No device or power requests.</p>
      <nav style={{ display: 'flex', gap: 16, margin: '20px 0' }}>
        <button
          onClick={() => {
            send(true);
            setTimeout(() => send(false), 8);
          }}
        >
          8 ms blink
        </button>
        <button
          onClick={() => {
            send(true);
            send(false);
          }}
        >
          Combined on/off
        </button>
        <button onClick={() => send(true)}>Activity on</button>
        <button onClick={() => send(false)}>Activity off</button>
        <button onClick={() => currentSource()?.fail()}>Drop stream</button>
        <button onClick={() => setMounted((value) => !value)}>Toggle toolbar</button>
      </nav>
      {mounted && <Power />}
      <output data-testid="led-stats">{stats}</output>
      <p data-testid="flash-result">{flash}</p>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);
