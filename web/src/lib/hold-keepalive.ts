// A key held down here stays down on the host until a key-up report reaches
// it. When the connection stalls, none does: a frozen tab or a network that
// drops without closing the socket leaves the server waiting for its heartbeat
// timeout, and the host auto-repeats the key until then.
//
// So while any key or mouse button is down this sends a keepalive every
// HOLD_KEEPALIVE_MS, and the server releases what is held when the keepalives
// stop (server/service/ws/hold_lease.go, 300 ms). Nothing is sent while
// nothing is held.
//
// The message type numbers are the socket's (lib/websocket.ts). They are
// repeated here so this file has no imports and the node test runner can load
// it as it is.

export const HOLD_KEEPALIVE_MS = 50;

export const KEEPALIVE_MESSAGE = 4;

const KEYBOARD_MESSAGE = 1;
const MOUSE_MESSAGE = 2;

// The mouse report lengths, without the message type byte.
const RELATIVE_MOUSE_LEN = 5;
const ABSOLUTE_MOUSE_LEN = 7;

type Kind = 'keyboard' | 'relative' | 'absolute';

// heldBy reads one outgoing message and says which input it describes and
// whether that input holds anything down. Anything else is null.
export function heldBy(message: ArrayLike<number>): { kind: Kind; held: boolean } | null {
  const report = Array.from(message);
  switch (report[0]) {
    case KEYBOARD_MESSAGE: {
      if (report.length !== 9) return null;
      // Modifiers, then a reserved byte, then six key codes.
      const held = report[1] !== 0 || report.slice(3).some((key) => key !== 0);
      return { kind: 'keyboard', held };
    }
    case MOUSE_MESSAGE: {
      const len = report.length - 1;
      if (len === RELATIVE_MOUSE_LEN || len === RELATIVE_MOUSE_LEN - 1)
        return { kind: 'relative', held: report[1] !== 0 };
      if (len === ABSOLUTE_MOUSE_LEN || len === ABSOLUTE_MOUSE_LEN - 1)
        return { kind: 'absolute', held: report[1] !== 0 };
      return null;
    }
    default:
      return null;
  }
}

export type KeepaliveTimers = {
  setInterval: (cb: () => void, ms: number) => unknown;
  clearInterval: (id: unknown) => void;
};

const browserTimers: KeepaliveTimers = {
  setInterval: (cb, ms) => setInterval(cb, ms),
  clearInterval: (id) => clearInterval(id as ReturnType<typeof setInterval>)
};

export class HoldKeepalive {
  private readonly held = new Set<Kind>();
  private timer: unknown = null;
  private readonly send: () => void;
  private readonly timers: KeepaliveTimers;
  private readonly interval: number;

  // The fields are assigned by hand: node's type stripping, which runs the
  // tests, does not accept constructor parameter properties.
  constructor(
    send: () => void,
    timers: KeepaliveTimers = browserTimers,
    interval = HOLD_KEEPALIVE_MS
  ) {
    this.send = send;
    this.timers = timers;
    this.interval = interval;
  }

  // observe follows a message that was sent, and starts or stops the
  // keepalive to match what it holds.
  public observe(message: ArrayLike<number>): void {
    const state = heldBy(message);
    if (!state) return;

    if (state.held) {
      this.held.add(state.kind);
    } else {
      this.held.delete(state.kind);
    }

    if (this.held.size > 0) {
      this.start();
    } else {
      this.stop();
    }
  }

  // reset forgets everything held. A closed socket has had its keys released
  // by the server, and a new one starts with nothing down.
  public reset(): void {
    this.held.clear();
    this.stop();
  }

  public get active(): boolean {
    return this.timer !== null;
  }

  private start(): void {
    if (this.timer !== null) return;
    this.timer = this.timers.setInterval(this.send, this.interval);
  }

  private stop(): void {
    if (this.timer === null) return;
    this.timers.clearInterval(this.timer);
    this.timer = null;
  }
}
