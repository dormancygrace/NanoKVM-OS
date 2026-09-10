import { useEffect } from 'react';
import { AttachAddon } from '@xterm/addon-attach';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal as XtermTerminal } from '@xterm/xterm';
import { useTranslation } from 'react-i18next';

import '@xterm/xterm/css/xterm.css';

import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { Head } from '@/components/head.tsx';

import { buildPicocomCommand } from './validater.ts';

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

    const url = `${getBaseUrl('ws')}/api/vm/terminal`;
    const ws = new WebSocket(url);
    let isPicocomRunning = false;
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
      runPicocom();
    };

    const sendSize = () => {
      if (disposed || ws.readyState !== WebSocket.OPEN) return;
      const windowSize = { rows: terminal.rows, cols: terminal.cols };
      const blob = new Blob([JSON.stringify(windowSize)], { type: 'application/json' });
      ws.send(blob);
    };

    const runPicocom = () => {
      const urls = window.location.href.split('?');
      if (urls.length < 2) return;

      const searchParams = new URLSearchParams(urls[1]);
      const port = searchParams.get('port');
      const baud = searchParams.get('baud');
      const parity = searchParams.get('parity');
      const flowControl = searchParams.get('flowControl');
      const dataBits = searchParams.get('dataBits');
      const stopBits = searchParams.get('stopBits');
      if (!port || !baud) return;

      const command = buildPicocomCommand({ port, baud, parity, flowControl, dataBits, stopBits });
      if (!command || disposed || ws.readyState !== WebSocket.OPEN) return;
      ws.send(command);

      isPicocomRunning = true;
    };

    const exitPicocom = () => {
      if (ws.readyState === WebSocket.OPEN && isPicocomRunning) {
        ws.send('\x01\x18');
        isPicocomRunning = false;
      }
    };

    const resizeScreen = () => {
      fitAddon.fit();
      sendSize();
    };

    const cleanupConnection = () => {
      disposed = true;
      exitPicocom();
      // WebSocket close follows already queued data, including picocom exit.
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
