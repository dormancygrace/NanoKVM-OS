// How long to wait before reconnecting after the given number of failures
// in a row: 2 s, then doubling to a minute, with a fifth of jitter either
// way so many tabs do not retry together. Each USB audio attempt is a new
// WebSocket and often a full WebRTC and DTLS setup on the device's single
// core; a failure must not bring the next one at once.
export function reconnectDelay(failures: number, random: () => number = Math.random): number {
  if (failures <= 0) return 0;
  const base = Math.min(60000, 2000 * 2 ** (failures - 1));
  return Math.round(base * (0.8 + 0.4 * random()));
}

// When the next automatic start may run: after a failure, not before its
// delay; after a connection (or a fresh start by the user), at once.
export class ReconnectGate {
  private failures = 0;
  private notBefore = 0;

  failed(now: number, random: () => number = Math.random) {
    this.failures += 1;
    this.notBefore = now + reconnectDelay(this.failures, random);
  }

  connected() {
    this.failures = 0;
    this.notBefore = 0;
  }

  allows(now: number) {
    return now >= this.notBefore;
  }
}
