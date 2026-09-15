export class LatestValueQueue<T> {
  private latest: T | undefined;
  private draining: Promise<void> | undefined;
  private readonly write: (value: T) => Promise<void>;

  constructor(write: (value: T) => Promise<void>) {
    this.write = write;
  }

  enqueue(value: T): Promise<void> {
    this.latest = value;
    if (!this.draining) {
      this.draining = this.drain().finally(() => {
        this.draining = undefined;
        // Do not lose a value queued between the drain's final check and cleanup.
        if (this.latest !== undefined) void this.enqueue(this.latest);
      });
    }
    return this.draining;
  }

  private async drain() {
    while (this.latest !== undefined) {
      const value = this.latest;
      this.latest = undefined;
      await this.write(value);
    }
  }
}
