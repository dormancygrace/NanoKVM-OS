import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';

import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { getBaseUrl } from '@/lib/service.ts';

import { readShellSession, storeShellSession } from './launch.ts';

export type ShellText = {
  reconnecting: string;
  takenOver: string;
  closed: string;
  ended: string;
  gone: string;
  limit: string;
};

// Close codes of server/service/vm/terminal_session.go.
const sessionRevoked = 4401;
const shellNotFound = 4404;
const shellTakenOver = 4409;
const shellClosed = 4410;
const shellEnded = 4411;
const shellLimit = 4429;

const retryDelays = [1000, 2000, 4000, 8000];

// attachShell connects the terminal to this tab's shell on the NanoKVM, or to
// a new one. The shell outlives the connection: after a dropped network the
// page attaches again by itself and the server replays the recent output.
export function attachShell(
  terminal: Terminal,
  fitAddon: FitAddon,
  element: HTMLElement,
  text: ShellText
) {
  let ws: WebSocket | null = null;
  let disposed = false;
  let retries = 0;
  let retryTimer = 0;
  // What Enter does while the connection is down.
  let onEnter: (() => void) | null = null;

  const notice = (message: string) => terminal.writeln(`\r\n\x1b[33m${message}\x1b[0m`);

  const sendSize = () => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    const size = JSON.stringify({ rows: terminal.rows, cols: terminal.cols });
    ws.send(new Blob([size], { type: 'application/json' }));
  };

  const connect = () => {
    window.clearTimeout(retryTimer);
    onEnter = null;
    const id = readShellSession();
    const query = id ? `?session=${encodeURIComponent(id)}` : '';
    const socket = new WebSocket(`${getBaseUrl('ws')}/api/vm/terminal${query}`);
    socket.binaryType = 'arraybuffer';
    ws = socket;

    socket.onopen = () => {
      if (disposed) socket.close();
    };
    socket.onmessage = (event: MessageEvent<ArrayBuffer | string>) => {
      if (typeof event.data === 'string') {
        // The first message names the shell; the replay of its recent output
        // follows, so the screen starts over.
        const hello = JSON.parse(event.data) as { session?: string };
        if (hello.session) {
          storeShellSession(hello.session);
          retries = 0;
          terminal.reset();
          sendSize();
        }
        return;
      }
      terminal.write(new Uint8Array(event.data));
    };
    socket.onclose = (event) => {
      if (disposed || ws !== socket) return;
      ws = null;
      switch (event.code) {
        case sessionRevoked:
          notifyAuthExpired();
          return;
        case shellTakenOver:
          notice(text.takenOver);
          onEnter = connect;
          return;
        case shellClosed:
        case shellEnded:
        case shellNotFound:
          storeShellSession('');
          notice(
            event.code === shellClosed
              ? text.closed
              : event.code === shellEnded
                ? text.ended
                : text.gone
          );
          onEnter = connect;
          return;
        case shellLimit:
          notice(text.limit);
          onEnter = connect;
          return;
      }
      // The network or the page's connection dropped: the shell is waiting.
      notice(text.reconnecting);
      onEnter = connect;
      retryTimer = window.setTimeout(
        connect,
        retryDelays[Math.min(retries, retryDelays.length - 1)]
      );
      retries += 1;
    };
  };

  const input = terminal.onData((data) => {
    if (ws?.readyState === WebSocket.OPEN) ws.send(data);
  });
  const enter = terminal.onKey(({ domEvent }) => {
    if (domEvent.key === 'Enter' && onEnter) onEnter();
  });
  const resize = () => {
    fitAddon.fit();
    sendSize();
  };
  const observer = new ResizeObserver(resize);
  observer.observe(element);
  window.addEventListener('resize', resize, false);

  connect();

  return {
    dispose() {
      disposed = true;
      window.clearTimeout(retryTimer);
      observer.disconnect();
      window.removeEventListener('resize', resize, false);
      input.dispose();
      enter.dispose();
      // The shell keeps running on the device and can be opened again.
      ws?.close();
      ws = null;
    }
  };
}
