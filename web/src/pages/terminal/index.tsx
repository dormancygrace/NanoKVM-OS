import { useEffect } from 'react';
import { AttachAddon } from '@xterm/addon-attach';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal as XtermTerminal } from '@xterm/xterm';
import { useTranslation } from 'react-i18next';

import '@xterm/xterm/css/xterm.css';

import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { Head } from '@/components/head.tsx';

import { readSerialSession } from './launch.ts';
import { buildSerialQuery } from './validater.ts';

export const Terminal = () => {
  const { t } = useTranslation();

  useEffect(() => {
    const terminalEle = document.getElementById('terminal');
    if (!terminalEle) return;

    const terminal = new XtermTerminal({
      cursorBlink: true
    });

    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(terminalEle);
    fitAddon.fit();

    const searchParams = readSerialSession();
    let query = '';
    if (searchParams.has('port')) {
      const serialQuery = buildSerialQuery({
        port: searchParams.get('port'),
        baud: searchParams.get('baud'),
        parity: searchParams.get('parity'),
        flowControl: searchParams.get('flowControl'),
        dataBits: searchParams.get('dataBits'),
        stopBits: searchParams.get('stopBits')
      });
      if (serialQuery === null) {
        terminal.writeln(
          t('terminal.invalidParameters', { defaultValue: 'Invalid serial parameters.' })
        );
        return () => terminal.dispose();
      }
      query = `?${serialQuery}`;
    }
    const url = `${getBaseUrl('ws')}/api/vm/terminal${query}`;
    const ws = new WebSocket(url);
    let disposed = false;

    ws.addEventListener('close', (event) => {
      if (event.code === 4401) {
        notifyAuthExpired();
      }
    });

    ws.onopen = () => {
      if (disposed) {
        ws.close();
        return;
      }
      const attachAddon = new AttachAddon(ws);
      terminal.loadAddon(attachAddon);

      sendSize();
    };

    const sendSize = () => {
      if (disposed || ws.readyState !== WebSocket.OPEN) return;
      const windowSize = { rows: terminal.rows, cols: terminal.cols };
      const blob = new Blob([JSON.stringify(windowSize)], { type: 'application/json' });
      ws.send(blob);
    };

    const resizeScreen = () => {
      fitAddon.fit();
      sendSize();
    };

    const cleanupConnection = () => {
      disposed = true;
      // The server owns and terminates the direct serial process on close.
      if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
        ws.close();
      }
    };

    const handleBeforeUnload = () => {
      cleanupConnection();
    };

    window.addEventListener('resize', resizeScreen, false);
    window.addEventListener('beforeunload', handleBeforeUnload);

    return () => {
      cleanupConnection();
      terminal.dispose();

      window.removeEventListener('resize', resizeScreen, false);
      window.removeEventListener('beforeunload', handleBeforeUnload);
    };
  }, []);

  return (
    <>
      <Head title={t('head.terminal')} />

      <div className="h-full w-full overflow-hidden">
        <div id="terminal" className="h-full p-2"></div>
      </div>
    </>
  );
};
