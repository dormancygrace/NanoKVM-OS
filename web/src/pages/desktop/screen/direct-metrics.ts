// Opt-in diagnostics: bounded numeric observations, never pixels or payloads.
class Distribution {
  private values: number[] = [];
  add(value: number) {
    if (this.values.length < 4096) this.values.push(value);
  }
  drain() {
    const v = this.values.sort((a, b) => a - b);
    this.values = [];
    const percentile = (p: number) =>
      v.length ? v[Math.min(v.length - 1, Math.floor((v.length - 1) * p))] : 0;
    return {
      count: v.length,
      p50: percentile(0.5),
      p95: percentile(0.95),
      max: v[v.length - 1] ?? 0
    };
  }
}

export class DirectMetrics {
  private last = new Map<string, number>();
  private gaps = new Map<string, Distribution>();
  private counts: Record<string, number> = {};
  private started = performance.now();
  private receiveTimes = new Map<number, number>();
  private residence = new Distribution();
  private decodeResidence = new Distribution();

  count(name: string, amount = 1) {
    this.counts[name] = (this.counts[name] ?? 0) + amount;
  }
  event(name: string, now = performance.now()) {
    this.count(name);
    const previous = this.last.get(name);
    if (previous !== undefined) {
      if (!this.gaps.has(name)) this.gaps.set(name, new Distribution());
      this.gaps.get(name)!.add(now - previous);
    }
    this.last.set(name, now);
  }
  receive(timestamp: number) {
    const now = performance.now();
    this.event('received', now);
    this.receiveTimes.set(timestamp, now);
    while (this.receiveTimes.size > 256)
      this.receiveTimes.delete(this.receiveTimes.keys().next().value!);
  }
  decode(timestamp: number) {
    const now = performance.now();
    this.event('decoded', now);
    const received = this.receiveTimes.get(timestamp);
    if (received !== undefined) this.decodeResidence.add(now - received);
  }
  paint(timestamp: number) {
    const now = performance.now();
    this.event('painted', now);
    const received = this.receiveTimes.get(timestamp);
    if (received !== undefined) this.residence.add(now - received);
    this.receiveTimes.delete(timestamp);
  }
  snapshot(extra: Record<string, number | string>) {
    const now = performance.now();
    const result = {
      seconds: (now - this.started) / 1000,
      counts: this.counts,
      gapsMs: Object.fromEntries(Array.from(this.gaps, ([name, dist]) => [name, dist.drain()])),
      receiveToPaintMs: this.residence.drain(),
      receiveToDecodeMs: this.decodeResidence.drain(),
      ...extra
    };
    this.counts = {};
    this.started = now;
    return result;
  }
}
