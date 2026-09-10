// Timestamps are sender-relative microseconds. Map them to the receiver clock;
// no synchronization between the two machines is assumed.
export type TimedFrame = { timestamp: number; close(): void };

export class DirectPlayout<T extends TimedFrame> {
  private frames: T[] = [];
  private origin: number | null = null;
  private lastTimestamp: number | null = null;
  dropped = 0;

  constructor(
    private readonly delayMs = 35,
    private readonly capacity = 6
  ) {}

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
  }
}
