import { FitAddon } from '@xterm/addon-fit';
import { Terminal } from '@xterm/xterm';

import { getRecheck } from '@/api/auth.ts';
import { notifyAuthExpired } from '@/lib/auth-events.ts';
import { requestRecheck } from '@/lib/recheck.ts';
import { getBaseUrl } from '@/lib/service.ts';

import { readShellSession, storeShellSession } from './launch.ts';

export type ShellText = {
  reconnecting: string;
  takenOver: string;
  closed: string;
  ended: string;
  gone: string;
  limit: string;
  declined: string;
};

// Close codes of server-cpp/src/system/terminal.cpp.
const sessionRevoked = 4401;
const shellNotFound = 4404;
const shellTakenOver = 4409;
const shellClosed = 4410;
const shellEnded = 4411;
const shellLimit = 4429;

const retryDelays = [1000, 2000, 4000, 8000];
// The terminal's answers to the queries in one chunk of output, at most:
// with JSON escaping well inside the server's 64 KiB message limit.
const maxAnswers = 4096;

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
  // The output of the current connection, by offset in the shell's output:
  // written to the terminal up to `written`, processed by it up to
  // `processed`, and the chunks in between, each with the terminal's answers
  // to the queries in it so far. Answers in a muted chunk are dropped: a page
  // processed it before and answered.
  type Output = {
    written: number;
    processed: number;
    acked: number;
    chunks: { end: number; mute: boolean; answers: string }[];
    replayNext: boolean;
    replayPending: number;
  };
  let output: Output | null = null;
  let ackTimer = 0;

  const notice = (message: string) => terminal.writeln(`\r\n\x1b[33m${message}\x1b[0m`);

  const send = (message: object) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(new Blob([JSON.stringify(message)], { type: 'application/json' }));
  };
  const sendSize = () => send({ rows: terminal.rows, cols: terminal.cols });

  // Tells the server how far the terminal got, so that a page attaching
  // later answers the queries this one did not.
  const sendAck = () => {
    ackTimer = 0;
    if (output && output.processed > output.acked) {
      output.acked = output.processed;
      send({ ack: output.processed });
    }
  };

  const write = (current: Output, data: Uint8Array, mute: boolean) => {
    if (data.length === 0) return;
    const end = current.written + data.length;
    current.written = end;
    const chunk = { end, mute, answers: '' };
    current.chunks.push(chunk);
    terminal.write(data, () => {
      current.chunks.shift();
      current.processed = end;
      if (current !== output) return;
      // The chunk is processed: its answers go out with its end, in one
      // message, so the server never has the answers without the offset or
      // the offset without the answers.
      if (chunk.answers) {
        send({ data: chunk.answers, ack: end });
        current.acked = Math.max(current.acked, end);
      } else if (!ackTimer) {
        ackTimer = window.setTimeout(sendAck, 100);
      }
    });
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
        const hello = JSON.parse(event.data) as {
          session?: string;
          offset?: number;
          replay?: number;
          pending?: number;
        };
        if (hello.session) {
          storeShellSession(hello.session);
          retries = 0;
          terminal.reset();
          const offset = hello.offset ?? 0;
          output = {
            written: offset,
            processed: offset,
            acked: offset,
            chunks: [],
            replayNext: (hello.replay ?? 0) > 0,
            replayPending: hello.pending ?? 0
          };
          sendSize();
        }
        return;
      }
      if (!output) return;
      const data = new Uint8Array(event.data);
      if (!output.replayNext) {
        write(output, data, false);
        return;
      }
      // A page processed the start of the replay and answered its terminal
      // queries (cursor position and the like): answering again would reach
      // the shell as input. No page processed the end, whether it was output
      // while detached or reached a page that went away first; a program may
      // still wait for the answers to its queries.
      output.replayNext = false;
      const answered = data.length - Math.min(output.replayPending, data.length);
      write(output, data.subarray(0, answered), true);
      write(output, data.subarray(answered), false);
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
          onEnter = () => void reconnect();
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
          onEnter = () => void reconnect();
          return;
        case shellLimit:
          notice(text.limit);
          onEnter = () => void reconnect();
          return;
      }
      // The network or the page's connection dropped: the shell is waiting.
      if (retries === 0) notice(text.reconnecting);
      retry();
    };
  };

  const retry = () => {
    onEnter = reconnect;
    retryTimer = window.setTimeout(
      reconnect,
      retryDelays[Math.min(retries, retryDelays.length - 1)]
    );
    retries += 1;
  };

  // The server opens a shell only within a few minutes of a password
  // confirmation, reattaching included (the shell acts as root). Past that it
  // refuses the socket, so confirm first, as the page did when it opened.
  const reconnect = async () => {
    window.clearTimeout(retryTimer);
    onEnter = null;
    const fresh = await getRecheck()
      .then((rsp) => (rsp.code === 0 ? rsp.data.fresh : null))
      .catch(() => null);
    if (disposed) return;
    if (fresh === null) {
      // The NanoKVM is still out of reach.
      retry();
      return;
    }
    if (!fresh && !(await requestRecheck())) {
      if (disposed) return;
      notice(text.declined);
      onEnter = () => void reconnect();
      return;
    }
    if (!disposed) connect();
  };

  // The terminal's data is either user input or its answers to queries in the
  // output. xterm marks user input at the source: its core fires onUserInput
  // right before onData for what the user types, pastes, composes (an IME
  // commit arrives in a timer after compositionend) or does with the mouse.
  // Answers come without it, while the terminal parses a chunk; so do focus
  // reports, which the user's focus change (a FocusEvent) sends as well.
  // xterm 6.0.0 offers this only on its core service.
  const core = (
    terminal as unknown as {
      _core: { coreService: { onUserInput: (listener: () => void) => { dispose(): void } } };
    }
  )._core.coreService;
  let userInput = false;
  const userInputMark = core.onUserInput(() => {
    userInput = true;
  });

  // User input goes out as text at once, without moving the processed
  // offset. Answers wait for the end of their chunk and go out with it.
  const input = terminal.onData((data) => {
    const fromUser = userInput || (window as { event?: Event }).event instanceof FocusEvent;
    userInput = false;
    if (!output || !ws || ws.readyState !== WebSocket.OPEN) return;
    const chunk = output.chunks[0];
    if (fromUser || !chunk) {
      ws.send(data);
      return;
    }
    // A page processed this chunk before and answered its queries.
    if (chunk.mute) return;
    if (chunk.answers.length + data.length > maxAnswers) {
      // Far beyond any real answer: pass it on without the offset, so a
      // page attaching later may answer the same queries again.
      ws.send(chunk.answers + data);
      chunk.answers = '';
      return;
    }
    chunk.answers += data;
  });
  // Mouse reports in the default (X10) encoding are bytes, not text: a
  // column or row past 95 is a byte above 127. They go out as a binary
  // message of their own, which starts with ESC, unlike the JSON messages.
  const mouse = terminal.onBinary((data) => {
    if (!ws || ws.readyState !== WebSocket.OPEN) return;
    ws.send(Uint8Array.from(data, (c) => c.charCodeAt(0) & 0xff));
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
      window.clearTimeout(ackTimer);
      observer.disconnect();
      window.removeEventListener('resize', resize, false);
      input.dispose();
      userInputMark.dispose();
      mouse.dispose();
      enter.dispose();
      // The shell keeps running on the device and can be opened again.
      ws?.close();
      ws = null;
    }
  };
}
