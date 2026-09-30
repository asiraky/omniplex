// Decides whether a server that exited on its own gets restarted, and after
// how long. Crashes in quick succession back off and eventually give up so the
// window can show an error instead of a spinner forever; a server that ran for
// a while before dying starts the count again.
export interface RestartOptions {
  maxRestarts: number;
  baseDelayMs: number;
  maxDelayMs: number;
  stableAfterMs: number;
}

export const DEFAULT_RESTART: RestartOptions = {
  maxRestarts: 4,
  baseDelayMs: 1000,
  maxDelayMs: 15000,
  stableAfterMs: 60000,
};

export class RestartPolicy {
  private attempts = 0;
  private startedAt = 0;

  constructor(
    private readonly opts: RestartOptions = DEFAULT_RESTART,
    private readonly now: () => number = Date.now,
  ) {}

  started(): void {
    this.startedAt = this.now();
  }

  // Milliseconds to wait before the next start, or null to give up.
  onExit(): number | null {
    if (this.startedAt && this.now() - this.startedAt >= this.opts.stableAfterMs) {
      this.attempts = 0;
    }
    this.attempts++;
    if (this.attempts > this.opts.maxRestarts) return null;
    return Math.min(this.opts.baseDelayMs * 2 ** (this.attempts - 1), this.opts.maxDelayMs);
  }

  reset(): void {
    this.attempts = 0;
    this.startedAt = 0;
  }
}
