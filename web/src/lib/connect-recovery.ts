// A lost connection usually means the device is rebooting. An attempt made while it is
// unreachable gets no reply, so the browser holds it in TCP SYN backoff for up to ~15 s
// and queues every other WebSocket to the host behind it. For RECOVERY_WINDOW after a
// loss, an attempt is abandoned after twice the time the lost socket took to open (at
// least 2 s), and the page retries soon. A page's first connect has no measured open
// time and keeps the browser's own timeout, so a slow link is never cut off on first
// load. A link that became much slower since the loss (a laptop moving from LAN to
// cellular) is cut off until the window ends. The timeout does not grow per attempt:
// that would leave a retry hanging in SYN backoff when the device comes back. The
// timeout starts when the socket is created, so a socket queued behind another one to
// the same host may be cut once on a slow link.
//
// The input socket, the WebRTC signaling socket, the Direct stream and the tiled MJPEG
// stream each keep their own ConnectRecovery.
const MIN_CONNECT_TIMEOUT = 2 * 1000;
const RECOVERY_WINDOW = 30 * 1000;
// The retry delay while recovering: about two TLS handshakes a second per socket, which
// a device refusing connections (503 in video recovery) can absorb.
export const RECOVERY_RECONNECT_DELAY = 500;

export class ConnectRecovery {
  private openMs: number | null = null;
  private lostAt: number | null = null;

  public opened(openMs: number): void {
    this.openMs = openMs;
    this.lostAt = null;
  }

  // A socket that had opened ended.
  public lost(now: number): void {
    this.lostAt ??= now;
  }

  public recovering(now: number): boolean {
    return this.openMs !== null && this.lostAt !== null && now - this.lostAt < RECOVERY_WINDOW;
  }

  // The connect timeout of an attempt that starts now, or null for none.
  public connectTimeout(now: number): number | null {
    return this.recovering(now) ? Math.max(MIN_CONNECT_TIMEOUT, 2 * this.openMs!) : null;
  }
}
