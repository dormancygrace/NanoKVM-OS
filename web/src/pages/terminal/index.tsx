import { useEffect, useEffectEvent, useState } from 'react';
import { AttachAddon } from '@xterm/addon-attach';
import { FitAddon } from '@xterm/addon-fit';
import { Terminal as XtermTerminal } from '@xterm/xterm';
import { useTranslation } from 'react-i18next';

import '@xterm/xterm/css/xterm.css';

import { getRecheck } from '@/api/auth.ts';
import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { requestRecheck } from '@/lib/recheck.ts';
import { getBaseUrl } from '@/lib/service.ts';
import { Head } from '@/components/head.tsx';

import { readSerialSession } from './launch.ts';
import { attachShell, type ShellText } from './shell.ts';
import { buildSerialQuery } from './validater.ts';

// A shell acts as root: the session confirms its password first (the server
// refuses the socket otherwise), then the terminal opens.
export const Terminal = () => {
  const { t } = useTranslation();
  const [state, setState] = useState<'checking' | 'ready' | 'declined'>('checking');

  useEffect(() => {
    let active = true;
    void (async () => {
      const fresh = await getRecheck()
        .then((rsp) => rsp.code === 0 && rsp.data.fresh)
        .catch(() => false);
      const confirmed = fresh || (await requestRecheck());
      if (active) setState(confirmed ? 'ready' : 'declined');
    })();
    return () => {
      active = false;
    };
  }, []);

  if (state === 'ready') return <TerminalSession />;
  return (
    <>
      <Head title={t('head.terminal')} />
      {state === 'declined' && (
        <div className="text-fg-muted flex h-full w-full items-center justify-center p-4 text-sm">
          {t('recheck.declined')}
        </div>
      )}
    </>
  );
};

const TerminalSession = () => {
  const { t } = useTranslation();
  const invalidParameters = useEffectEvent(() =>
    t('terminal.invalidParameters', { defaultValue: 'Invalid serial parameters.' })
  );
  const disconnected = useEffectEvent(() => t('terminal.disconnected'));
  const shellText = useEffectEvent((): ShellText => ({
    reconnecting: t('terminal.reconnecting'),
    takenOver: t('terminal.takenOver'),
    closed: t('terminal.sessionClosed'),
    ended: t('terminal.sessionEnded'),
    gone: t('terminal.sessionGone'),
    limit: t('terminal.sessionLimit'),
    declined: t('terminal.recheckDeclined')
  }));

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
    if (!searchParams.has('port')) {
      const shell = attachShell(terminal, fitAddon, terminalEle, shellText());
      return () => {
        shell.dispose();
        terminal.dispose();
      };
    }
    // A serial terminal: a direct bridge to the port that ends with the page.
    const serialQuery = buildSerialQuery({
      port: searchParams.get('port'),
      baud: searchParams.get('baud'),
      parity: searchParams.get('parity'),
      flowControl: searchParams.get('flowControl'),
      dataBits: searchParams.get('dataBits'),
      stopBits: searchParams.get('stopBits')
    });
    if (serialQuery === null) {
      terminal.writeln(invalidParameters());
      return () => terminal.dispose();
    }
    const url = `${getBaseUrl('ws')}/api/vm/terminal?${serialQuery}`;
    const ws = new WebSocket(url);
    let disposed = false;
    let resizeNonce = '';
    const resizeProtocol = terminal.parser.registerOscHandler(777, (data) => {
      const match = /^nkos-resize;1;([a-f0-9]{32})$/.exec(data);
      if (!match) return false;
      resizeNonce = match[1];
      sendSize();
      return true;
    });

    ws.addEventListener('close', (event) => {
      if (event.code === 4401) {
        notifyAuthExpired();
        return;
      }
      if (disposed) return;
      // Any other close ends the session: say so and offer a reconnect. A
      // reload keeps the serial parameters, which live in sessionStorage.
      terminal.writeln(`\r\n\x1b[31m${disconnected()}\x1b[0m`);
      terminal.onKey(({ domEvent }) => {
        if (domEvent.key === 'Enter') window.location.reload();
      });
    });

    ws.onopen = () => {
      if (disposed) {
        ws.close();
        return;
      }
      const attachAddon = new AttachAddon(ws);
      terminal.loadAddon(attachAddon);

      sendSize();
      // Wake the USB host's login prompt once per connection.
      if (searchParams.get('port') === '/dev/ttyGS0') ws.send('\r');
    };

    const sendSize = () => {
      if (disposed || ws.readyState !== WebSocket.OPEN) return;
      const windowSize = { rows: terminal.rows, cols: terminal.cols };
      const blob = new Blob([JSON.stringify(windowSize)], { type: 'application/json' });
      ws.send(blob);
      if (resizeNonce) {
        ws.send(`\x1b]777;nkos-size;1;${resizeNonce};${terminal.rows};${terminal.cols}\x07`);
      }
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

    const observer = new ResizeObserver(resizeScreen);
    observer.observe(terminalEle);
    window.addEventListener('resize', resizeScreen, false);
    window.addEventListener('beforeunload', handleBeforeUnload);

    return () => {
      cleanupConnection();
      observer.disconnect();
      resizeProtocol.dispose();
      terminal.dispose();

      window.removeEventListener('resize', resizeScreen, false);
      window.removeEventListener('beforeunload', handleBeforeUnload);
    };
  }, []);

  return (
    <>
      <Head title={t('head.terminal')} />

      <div className="h-full w-full overflow-hidden bg-black">
        <div id="terminal" className="h-full p-2"></div>
      </div>
    </>
  );
};
