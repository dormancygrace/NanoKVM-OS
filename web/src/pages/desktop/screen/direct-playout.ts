// Timestamps are sender-relative microseconds. Map them to the receiver clock;
// no synchronization between the two machines is assumed.
export type TimedFrame = { timestamp: number; close(): void };

export class DirectPlayout<T extends TimedFrame> {
  private frames: T[] = [];
  private origin: number | null = null;
  private lastTimestamp: number | null = null;
  private adaptiveDelayMs = 10;
  private targetDelayMs = 10;
  private lastSlew: number | null = null;
  private arrivals: { at: number; late: number }[] = [];
  private lastAdjustment: number | null = null;
  private lastIncrease = 0;
  dropped = 0;

  constructor(
    private readonly configuredDelay: number | 'adaptive' = 35,
    private readonly capacity = 6
  ) {}

  get delayMs() {
    return this.configuredDelay === 'adaptive' ? this.adaptiveDelayMs : this.configuredDelay;
  }

  get adaptive() {
    return this.configuredDelay === 'adaptive';
  }

  private observeArrival(now: number, lateness: number) {
    if (!this.adaptive) return;
    // Bounded, recent arrival history. A single long outage must not turn into
    // a permanently large playout delay. Never try to buffer seconds of video.
    this.arrivals.push({ at: now, late: Math.max(0, Math.min(250, lateness)) });
    while (this.arrivals.length > 240 || this.arrivals[0].at < now - 2000) this.arrivals.shift();
    if (this.lastAdjustment === null) {
      this.lastAdjustment = now;
      this.lastIncrease = now;
      return;
    }
    const elapsed = now - this.lastAdjustment;
    if (elapsed < 250 || this.arrivals.length < 8) return;
    this.lastAdjustment = now;
    const sorted = this.arrivals.map((sample) => sample.late).sort((a, b) => a - b);
    // Cover recurring tail jitter, not just the easiest 90% of arrivals.
    // The floor index excludes one isolated worst sample in a mature window.
    const p99 = sorted[Math.floor((sorted.length - 1) * 0.99)];
    const target = Math.max(10, Math.min(60, p99 + 5));
    if (Math.abs(target - this.targetDelayMs) > 2) this.targetDelayMs = target;
  }

  private slewDelay(now: number) {
    if (!this.adaptive) return;
    const elapsed = this.lastSlew === null ? 0 : Math.max(0, Math.min(50, now - this.lastSlew));
    this.lastSlew = now;
    if (this.targetDelayMs > this.adaptiveDelayMs) {
      // Spread increases over arrivals instead of shifting every deadline by
      // 10 ms at once. Clamp elapsed so a network outage cannot cause a jump.
      this.adaptiveDelayMs = Math.min(this.targetDelayMs, this.adaptiveDelayMs + elapsed * 0.04);
      this.lastIncrease = now;
    } else if (now - this.lastIncrease >= 3000) {
      this.adaptiveDelayMs = Math.max(this.targetDelayMs, this.adaptiveDelayMs - elapsed * 0.002);
    }
  }

  get size() {
    return this.frames.length;
  }

  push(frame: T, now: number) {
    const pts = frame.timestamp / 1000;
    // A reconnect / sender clock restart must not leave an old playout epoch.
    if (this.lastTimestamp !== null && frame.timestamp <= this.lastTimestamp) this.reset();
    this.lastTimestamp = frame.timestamp;
    if (this.origin === null) this.origin = now - pts;
    // Correct clock drift and early arrivals without accumulating latency.
    this.origin = Math.min(this.origin, now - pts);
    this.observeArrival(now, now - pts - this.origin);
    this.slewDelay(now);
    this.frames.push(frame);
    while (this.frames.length > this.capacity) {
      this.frames.shift()!.close();
      this.dropped++;
    }
  }

  take(now: number): T | undefined {
    if (this.origin === null || !this.frames.length) return;
    let selected: T | undefined;
    // After a late callback, show the newest due frame instead of replaying a
    // stale queue. Future frames stay queued to absorb arrival jitter.
    while (
      this.frames.length &&
      this.origin + this.frames[0].timestamp / 1000 + this.delayMs <= now
    ) {
      if (selected) {
        selected.close();
        this.dropped++;
      }
      selected = this.frames.shift();
    }
    return selected;
  }

  reset() {
    for (const frame of this.frames) frame.close();
    this.frames = [];
    this.origin = null;
    this.lastTimestamp = null;
    this.arrivals = [];
    this.lastAdjustment = null;
    this.lastIncrease = 0;
    this.adaptiveDelayMs = 10;
    this.targetDelayMs = 10;
    this.lastSlew = null;
  }
}
