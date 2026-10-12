// Formatting and chart-data helpers for the Usage page. Pure, so the
// bucketing, dedup and label rules are testable without a socket.

import type { QuotaWindow, UsageReport, UsageRow, UsageTotals } from "~/protocol";

/** Every bucket start in [from, to), including the ones with no usage: gaps
 *  render as zero rather than compressing time. */
export function bucketStarts(from: number, to: number, bucketMs: number): number[] {
  if (bucketMs <= 0 || to <= from) return [];
  const out: number[] = [];
  for (let t = bucketOf(from, bucketMs); t < to; t += bucketMs) out.push(t);
  return out;
}

/** The bucket a timestamp falls in, aligned the way the server floors them. */
export function bucketOf(ts: number, bucketMs: number): number {
  return ts - (ts % bucketMs);
}

export interface BucketedCell {
  start: number;
  provider: string;
  model: string;
  totals: UsageTotals;
}

/** Group report rows by bucket start, sorted by time. The rows arrive
 *  pre-bucketed; this only orders them for rendering. */
export function rowsByTime(rows: UsageRow[]): BucketedCell[] {
  return [...rows].sort((a, b) => a.start - b.start || (a.provider < b.provider ? -1 : 1));
}

/** Sum the token categories of a totals row — the Tokens view's metric. */
export function totalTokens(t: UsageTotals): number {
  return t.input + t.output + t.cacheRead + t.cacheWrite;
}

/** USD with the precision the number deserves: whole dollars and cents for
 *  the headline, significant digits for the fractions a small thread costs. */
export function formatCost(usd: number): string {
  if (!Number.isFinite(usd)) return "—";
  if (usd === 0) return "$0";
  if (usd >= 1000) return `$${Math.round(usd).toLocaleString()}`;
  if (usd >= 1) return `$${usd.toFixed(2)}`;
  // Below a dollar, cents are noise: two significant digits is what the
  // figure actually knows.
  const decimals = Math.min(6, 1 - Math.floor(Math.log10(usd)));
  return `$${usd.toFixed(decimals)}`;
}

/** Compact token counts: 1.2M, 840k, 12. */
export function formatTokens(n: number): string {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(1)}B`;
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(Math.round(n));
}

/** "3d 5h", "3h 20m", "12m" — a coarse duration, the unit the wait
 *  until a limit resets is read in. */
function formatDuration(ms: number): string {
  const totalMinutes = Math.ceil(ms / 60_000);
  if (totalMinutes <= 0) return "now";
  const days = Math.floor(totalMinutes / (24 * 60));
  const hours = Math.floor((totalMinutes % (24 * 60)) / 60);
  const minutes = totalMinutes % 60;
  if (days > 0) return hours === 0 ? `${days}d` : `${days}d ${hours}h`;
  if (hours === 0) return `${minutes}m`;
  return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
}

/** "3d 5h", "3h 20m", "12m", "now" — the wait until a limit resets. */
export function formatCountdown(ms: number, now: number): string {
  return ms <= now ? "now" : formatDuration(ms - now);
}

/** A quota reading older than this is stale: flagged, and never projected from. */
export const QUOTA_STALE_MS = 15 * 60_000;

/** Below this fraction of a window elapsed, a burn-rate projection is noise. */
const EARLY_ELAPSED = 0.05;

/** Where a window's linear burn rate leads by the time it resets. */
export type QuotaProjection =
  /** The limit is already reached. */
  | { kind: "reached" }
  /** The limit is hit before the reset, at epoch ms `at`. */
  | { kind: "hits"; at: number }
  /** The window resets first, with about `percent` used. */
  | { kind: "finishes"; percent: number };

export interface QuotaPace {
  /** Fraction of the window that has passed, 0–1: where the marker sits. */
  elapsed: number;
  /** Percentage points used beyond what the elapsed time allows; negative is
   *  headroom. Absent with no fresh reading to compare. */
  ahead?: number;
  /** Absent with no fresh reading, or too early in the window to mean anything. */
  projection?: QuotaProjection;
  /** The reading is too old to compare against the clock. */
  stale: boolean;
}

/**
 * Whether a window's usage is on pace with the time that has passed, and
 * where a linear burn rate puts it at reset. Undefined when there is no
 * timed window to measure against: a credits window, a missing reset time or
 * duration, or a window that has already reset.
 */
export function quotaPace(w: QuotaWindow, now: number): QuotaPace | undefined {
  if (w.kind === "credits" || !w.resetsAt || !w.windowDurationMins || w.resetsAt <= now) return undefined;
  const durationMs = w.windowDurationMins * 60_000;
  const elapsed = Math.max(0, Math.min(1, 1 - (w.resetsAt - now) / durationMs));
  const stale = !!w.checkedAt && now - w.checkedAt >= QUOTA_STALE_MS;
  const used = w.usedPercent;
  if (stale || used === undefined) return { elapsed, stale };

  const pace: QuotaPace = { elapsed, stale, ahead: used - elapsed * 100 };
  if (used >= 100) pace.projection = { kind: "reached" };
  else if (elapsed >= EARLY_ELAPSED) {
    // Linear: the rate so far, carried on to the reset.
    const finish = used / elapsed;
    if (finish > 100) {
      const elapsedMs = elapsed * durationMs;
      pace.projection = { kind: "hits", at: now + ((100 - used) / used) * elapsedMs };
    } else {
      pace.projection = { kind: "finishes", percent: finish };
    }
  }
  return pace;
}

/** "Tracking 15% ahead of pace", "12% under pace", "On pace". */
export function formatPace(ahead: number): string {
  const points = Math.round(Math.abs(ahead));
  if (points === 0) return "On pace";
  return ahead > 0 ? `Tracking ${points}% ahead of pace` : `${points}% under pace`;
}

/** "5m ago", "2h ago" — how long since a quota was observed. */
export function formatAge(ms: number, now: number): string {
  const age = now - ms;
  if (age < 60_000) return "just now";
  return `${formatDuration(age)} ago`;
}

/** The chart palette, one colour per provider, stable within a page. */
export function providerColor(provider: string, index: number): string {
  // Named providers keep their colour across every chart and legend on the
  // page; unknown ones fall through to the palette by position.
  const named: Record<string, string> = {
    claude: "var(--chart-1)",
    codex: "var(--chart-4)",
  };
  return named[provider] ?? `var(--chart-${((index % 5) + 1)})`;
}

export const RANGES = [
  { id: "24h", label: "24h" },
  { id: "7d", label: "7d" },
  { id: "30d", label: "30d" },
  { id: "90d", label: "90d" },
] as const;

export type RangeId = (typeof RANGES)[number]["id"];

export function bucketLabel(start: number, bucketMs: number): string {
  const d = new Date(start);
  if (bucketMs >= 24 * 3600 * 1000) {
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
  }
  return d.toLocaleTimeString(undefined, { hour: "numeric", hourCycle: "h23" });
}

/** Exact timestamp for a tooltip: "14:00, 12 Apr" for hours, "12 Apr" for days. */
export function bucketFullLabel(start: number, bucketMs: number): string {
  const d = new Date(start);
  if (bucketMs >= 24 * 3600 * 1000) {
    return d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric", year: "numeric" });
  }
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit", hourCycle: "h23" });
}

/** Whether a report contains usage the dollar total could not price. */
export function hasUnpriced(report: UsageReport): boolean {
  return report.totals.unpriced > 0;
}