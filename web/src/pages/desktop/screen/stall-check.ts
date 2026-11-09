// When the video stops, the page asks whether its signaling connection is still alive
// before it replaces it. Frames also stop while the connection is fine (the target
// reboots, changes resolution or loses its signal), so a frozen video first sends a
// heartbeat; only a heartbeat left unanswered leads to a probe with a new WebSocket.

// A video with no new frame this long is checked.
const STALL_AFTER = 1000;
// A live connection answers within one round trip; these floors cover a LAN, and both
// limits grow with the time the signaling socket took to open (about three round trips),
// so a slow link is not taken for a dead one.
const MIN_REPLY_WINDOW = 400;
const MIN_PROBE_TIMEOUT = 800;
// A device that refuses probes (503 in video recovery mode) costs one TLS handshake per
// probe, so probes start at most this often: about two a second, as signaling retries.
const PROBE_PACE = 500;

export const replyWindow = (openMs: number) => Math.max(MIN_REPLY_WINDOW, openMs);
export const probeTimeout = (openMs: number) => Math.max(MIN_PROBE_TIMEOUT, 2 * openMs);

type StallAction = 'none' | 'heartbeat' | 'probe';

export class StallCheck {
  private lastFrameAt = 0;
  private heartbeatSentAt = 0;
  private lastReplyAt = 0;
  private openMs = 0;

  public opened(openMs: number): void {
    this.openMs = openMs;
  }

  // The first frame arms the check, so a slow start is left to the connection timeout.
  public frame(now: number): void {
    this.lastFrameAt = now;
    this.heartbeatSentAt = 0;
  }

  // A hidden page presents no frames, so the check starts over when it is shown again.
  public shown(now: number): void {
    if (this.lastFrameAt) this.frame(now);
  }

  public reply(now: number): void {
    this.lastReplyAt = now;
  }

  public get heartbeatUnanswered(): boolean {
    return this.heartbeatSentAt > 0 && this.lastReplyAt < this.heartbeatSentAt;
  }

  public probeTimeout(): number {
    return probeTimeout(this.openMs);
  }

  // Called on every check tick.
  public step(now: number): StallAction {
    if (!this.frozen(now)) return 'none';
    if (!this.heartbeatSentAt) {
      this.heartbeatSentAt = now;
      return 'heartbeat';
    }
    if (!this.heartbeatUnanswered) {
      // Alive: check again after another STALL_AFTER without frames.
      this.frame(now);
      return 'none';
    }
    return now - this.heartbeatSentAt >= replyWindow(this.openMs) ? 'probe' : 'none';
  }

  // An opened probe shows the device is up. The old connection is dead only if the
  // video is still frozen and the heartbeat still unanswered: a frame or a late reply
  // while the probe opened means it recovered.
  public confirmsDead(now: number): boolean {
    return this.frozen(now) && this.heartbeatUnanswered;
  }

  private frozen(now: number): boolean {
    return this.lastFrameAt > 0 && now - this.lastFrameAt >= STALL_AFTER;
  }
}

// The socket a probe needs: a WebSocket in the page, a fake in tests.
type ProbeSocket = Pick<WebSocket, 'readyState' | 'close' | 'onopen' | 'onclose'>;

type StallProbeHost = {
  now: () => number;
  // The check runs only while this holds: signaling open, page visible, no reconnect
  // pending.
  canCheck: () => boolean;
  // The browser opens one WebSocket per host at a time, so while the input socket
  // reconnects a probe would hold its handshake back.
  inputConnected: () => boolean;
  heartbeat: () => void;
  openProbe: () => ProbeSocket;
  // The old connection is dead and the device is up.
  dead: () => void;
};

// Runs a StallCheck on a timer: sends its heartbeat, opens at most one probe at a time
// (PROBE_PACE apart), and reports a dead connection when an opened probe confirms it.
export class StallProbe {
  private probe: ProbeSocket | null = null;
  private nextProbeAt = 0;
  private disposed = false;

  constructor(
    private readonly stall: StallCheck,
    private readonly host: StallProbeHost
  ) {}

  public tick(): void {
    const { stall, host } = this;
    if (this.disposed) return;
    if (this.probe) {
      if (
        (!host.canCheck() || !host.inputConnected()) &&
        this.probe.readyState === WebSocket.CONNECTING
      ) {
        this.probe.close();
      }
      return;
    }
    if (!host.canCheck()) return;
    const now = host.now();
    const action = stall.step(now);
    if (action === 'heartbeat') host.heartbeat();
    if (action !== 'probe' || !host.inputConnected() || now < this.nextProbeAt) return;
    const current = host.openProbe();
    this.probe = current;
    this.nextProbeAt = now + PROBE_PACE;
    const timeout = setTimeout(() => current.close(), stall.probeTimeout());
    current.onopen = () => {
      current.close();
      if (
        !this.disposed &&
        host.canCheck() &&
        host.inputConnected() &&
        stall.confirmsDead(host.now())
      ) {
        host.dead();
      }
    };
    current.onclose = () => {
      clearTimeout(timeout);
      if (this.probe === current) this.probe = null;
    };
  }

  public dispose(): void {
    this.disposed = true;
    this.probe?.close();
  }
}
