import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { useSetAtom } from 'jotai';

import { client } from '@/lib/websocket';
import { isKeyboardOpenAtom } from '@/jotai/keyboard';
import { Head } from '@/components/head';
import { VirtualKeyboard } from '@/pages/desktop/virtual-keyboard';

export function Workbench() {
  const openKeyboard = useSetAtom(isKeyboardOpenAtom);
  const [mounted, setMounted] = useState(true);
  const [state, setState] = useState({ connections: 0, reports: [] as number[][] });
  const [title, setTitle] = useState('Browser fixture');
  const video = useRef<HTMLInputElement>(null);
  const status = useSyncExternalStore(client.subscribeConnectionStatus, client.getConnectionStatus);
  const control = useSyncExternalStore(client.subscribeControlStatus, client.getControlEnabled);
  useEffect(() => {
    client.setInputEnabled(true);
    client.connect();
    let active = true;
    const refresh = async () => {
      const next = await fetch('/__test/state').then((r) => r.json());
      if (active) setState(next);
    };
    const timer = window.setInterval(() => void refresh(), 250);
    return () => {
      active = false;
      clearInterval(timer);
      client.close();
      client.setInputEnabled(false);
    };
  }, []);
  return (
    <>
      <Head title={title} />
      <main className="max-w-full space-y-3 p-4 break-words">
        <h1>Browser interaction fixture</h1>
        <p>
          Production login, route guard, metadata, keyboard and WebSocket client. Local fixture
          backend only.
        </p>
        <label>
          Video focus target{' '}
          <input ref={video} aria-label="Video focus target" className="max-w-full border p-2" />
        </label>
        <div className="flex flex-wrap gap-4">
          <button onPointerDown={(e) => e.preventDefault()} onClick={() => openKeyboard(true)}>
            Open keyboard
          </button>
          <button
            onClick={() => {
              openKeyboard(false);
              setMounted(false);
            }}
          >
            Unmount keyboard
          </button>
          <button onClick={() => setMounted(true)}>Mount keyboard</button>
          <button onClick={() => void fetch('/__test/drop', { method: 'POST' })}>
            Drop connection
          </button>
          <button onClick={() => void fetch('/__test/expire', { method: 'POST' })}>
            Expire session
          </button>
          <button onClick={() => setTitle('Updated fixture')}>Change title</button>
        </div>
        <p data-testid="connection">
          {status} / {control ? 'control' : 'no control'}
        </p>
        <p data-testid="connections">Connections: {state.connections}</p>
        <pre className="max-w-full text-xs break-all whitespace-pre-wrap" data-testid="reports">
          HID: {JSON.stringify(state.reports)}
        </pre>
      </main>
      {mounted && <VirtualKeyboard />}
    </>
  );
}
