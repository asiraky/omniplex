import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ActivityIcon, CircleAlertIcon, ClockIcon, InfoIcon, RefreshCwIcon, XIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import { IconButton } from "~/components/IconButton";
import { Spinner } from "~/components/ui/spinner";
import {
  bucketFullLabel,
  bucketLabel,
  bucketStarts,
  formatAge,
  formatCountdown,
  formatCost,
  formatTokens,
  hasUnpriced,
  providerColor,
  RANGES,
  rowsByTime,
  totalTokens,
  type RangeId,
} from "~/lib/usageFormat";
import { cn } from "~/lib/utils";
import type { QuotaStatus, QuotaWindow, UsageReport } from "~/protocol";

type View = "cost" | "tokens" | "limits";

const VIEWS: { id: View; label: string }[] = [
  { id: "cost", label: "Cost" },
  { id: "tokens", label: "Tokens" },
  { id: "limits", label: "Limits" },
];

const VIEW_KEY = "omniplex.usageView";
const RANGE_KEY = "omniplex.usageRange";

function loadStored<T extends string>(key: string, valid: readonly T[], fallback: T): T {
  try {
    const v = localStorage.getItem(key) as T | null;
    if (v && valid.includes(v)) return v;
  } catch {
    // Storage can be blocked; the default is fine.
  }
  return fallback;
}

export interface UsageProps {
  quotas: QuotaStatus[];
  onRefreshQuota: (instance: string) => Promise<QuotaStatus[]>;
  onClose: () => void;
  loadReport: (range: string) => Promise<UsageReport>;
}

/**
 * The account-level Usage page: what work cost through the API (Cost),
 * what it consumed (Tokens), and whether there is allowance left to keep
 * working (Limits). A full-page destination on purpose — it answers
 * questions about the account, not about any one session, so it never needs
 * one attached.
 */
export function UsagePage({ quotas, onRefreshQuota, onClose, loadReport }: UsageProps) {
  const [view, setView] = useState<View>(() =>
    loadStored(VIEW_KEY, VIEWS.map((v) => v.id) as View[], "cost"),
  );
  const [range, setRange] = useState<RangeId>(() => loadStored(RANGE_KEY, RANGES.map((r) => r.id) as RangeId[], "24h"));
  const persistView = useCallback((v: View) => {
    setView(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch {}
  }, []);
  const persistRange = useCallback((r: RangeId) => {
    setRange(r);
    try {
      localStorage.setItem(RANGE_KEY, r);
    } catch {}
  }, []);

  const [report, setReport] = useState<UsageReport | null>(null);
  const [reportError, setReportError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  // Keyed by range so a slow reply for an old range cannot land over a newer
  // one after a flappy reconnect.
  const loadSeq = useRef(0);
  useEffect(() => {
    if (view === "limits") return;
    const seq = ++loadSeq.current;
    setLoading(true);
    setReportError(null);
    loadReport(range)
      .then((r) => {
        if (seq !== loadSeq.current) return;
        setReport(r);
      })
      .catch((e: Error) => {
        if (seq !== loadSeq.current) return;
        setReportError(e.message);
      })
      .finally(() => {
        if (seq === loadSeq.current) setLoading(false);
      });
  }, [view, range, loadReport]);

  return (
    <div className="bg-background fixed inset-0 z-50 flex flex-col">
      <header className="flex items-center gap-2 px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:px-4">
        <IconButton label="Close usage" onClick={onClose}>
          <XIcon />
        </IconButton>
        <h1 className="flex-1 text-[15px] font-semibold">Usage</h1>
        {/* Segmented view switcher: three destinations, one row, thumb-sized. */}
        <div role="tablist" aria-label="Usage views" className="bg-secondary/60 flex rounded-full p-0.5">
          {VIEWS.map((v) => (
            <button
              key={v.id}
              role="tab"
              aria-selected={view === v.id}
              onClick={() => persistView(v.id)}
              className={cn(
                "focus-visible:ring-ring rounded-full px-3 py-1.5 text-[12px] font-medium outline-none focus-visible:ring-2",
                view === v.id ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
              )}
            >
              {v.label}
            </button>
          ))}
        </div>
      </header>

      <div className="scroll-thin min-h-0 flex-1 overflow-y-auto px-3 pb-[calc(2rem+env(safe-area-inset-bottom))] md:px-4">
        {view === "limits" ? (
          <LimitsView quotas={quotas} onRefresh={onRefreshQuota} />
        ) : (
          <>
            <div className="mt-1 flex flex-wrap items-center gap-2">
              {RANGES.map((r) => (
                <button
                  key={r.id}
                  onClick={() => persistRange(r.id)}
                  aria-pressed={range === r.id}
                  className={cn(
                    "focus-visible:ring-ring rounded-md border px-2.5 py-1 text-[12px] font-medium outline-none focus-visible:ring-2",
                    range === r.id
                      ? "border-primary/40 bg-primary/10 text-foreground"
                      : "text-muted-foreground hover:text-foreground border-transparent",
                  )}
                >
                  Past {r.label}
                </button>
              ))}
              {loading && <Spinner className="text-muted-foreground/60 size-4" />}
            </div>

            {reportError ? (
              <div className="text-destructive mt-4 rounded-lg border p-3 text-[13px]">
                Could not load usage: {reportError}
              </div>
            ) : report ? (
              <HistoryView report={report} metric={view === "cost" ? "cost" : "tokens"} />
            ) : (
              !loading && <p className="text-muted-foreground mt-6 text-[13px]">No usage recorded yet.</p>
            )}
          </>
        )}
      </div>
    </div>
  );
}

/**
 * The Cost and Tokens views share one shape: a headline, a stacked
 * time-series split by provider, provider shares, and a model breakdown.
 * Only the metric changes — dollars or tokens — and every chart moves
 * together because they all read the same report.
 */
function HistoryView({ report, metric }: { report: UsageReport; metric: "cost" | "tokens" }) {
  const value = useCallback((t: typeof report.totals) => metric === "cost" ? t.cost : totalTokens(t), [metric]);
  const format = metric === "cost" ? formatCost : formatTokens;

  const providers = useMemo(() => {
    // Providers ordered by their total, so the legend and the stacks agree.
    const totals = new Map<string, number>();
    for (const row of report.rows) totals.set(row.provider, (totals.get(row.provider) ?? 0) + value(row.totals));
    return [...totals.entries()].sort((a, b) => b[1] - a[1]).map(([id]) => id);
  }, [report.rows, value]);

  const starts = useMemo(
    () => bucketStarts(report.from, report.to, report.bucketMs),
    [report.from, report.to, report.bucketMs],
  );
  // Rows grouped by bucket, with the gaps present as empty buckets.
  const buckets = useMemo(() => {
    const byStart = new Map<number, typeof report.rows>();
    for (const row of rowsByTime(report.rows)) {
      const list = byStart.get(row.start) ?? [];
      list.push(row);
      byStart.set(row.start, list);
    }
    return starts.map((start) => ({ start, rows: byStart.get(start) ?? [] }));
  }, [report.rows, starts]);

  const headline = value(report.totals);
  const unpriced = metric === "cost" && hasUnpriced(report);

  return (
    <div className="mt-3 flex flex-col gap-4">
      {/* Headline: the one number the page exists to answer. */}
      <div>
        <p className="text-3xl font-semibold tracking-tight tabular-nums">{format(headline)}</p>
        <p className="text-muted-foreground mt-1 text-[12px]">
          {metric === "cost" ? "API-equivalent cost" : "Tokens processed"} · past{" "}
          {report.range === "24h" ? "24 hours" : report.range.replace("d", " days")}
          {metric === "cost" && ` · priced as ${report.priceVersion}`}
        </p>
        {metric === "cost" && (
          <p className="text-muted-foreground/80 mt-0.5 text-[11px] leading-relaxed">
            What this usage would have cost through the API. It is not your subscription bill — you
            are not charged this amount.
          </p>
        )}
        {unpriced && (
          <p className="mt-2 flex items-start gap-1.5 rounded-md border border-attention/40 bg-attention-surface/60 px-2 py-1.5 text-[11px] leading-relaxed text-attention-foreground">
            <InfoIcon aria-hidden className="mt-0.5 size-3 shrink-0" />
            <span>
              {formatTokens(report.totals.unpriced)} tokens had no published price and are excluded
              from the total rather than counted as free.
            </span>
          </p>
        )}
      </div>

      {/* Provider shares: the legend is the chart, at a glance. */}
      {providers.length > 0 && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
          {providers.map((p, i) => {
            const total = report.rows.filter((r) => r.provider === p).reduce((sum, r) => sum + value(r.totals), 0);
            const share = headline > 0 ? Math.round((total / headline) * 100) : 0;
            return (
              <span key={p} className="flex items-center gap-1.5 text-[12px]">
                <span aria-hidden className="size-2.5 rounded-full" style={{ background: providerColor(p, i) }} />
                <span className="font-medium capitalize">{p}</span>
                <span className="text-muted-foreground tabular-nums">{share}%</span>
              </span>
            );
          })}
        </div>
      )}

      <StackedChart
        buckets={buckets}
        providers={providers}
        bucketMs={report.bucketMs}
        value={value}
        format={format}
        metric={metric}
      />

      <ModelBreakdown report={report} metric={metric} value={value} format={format} providers={providers} />
    </div>
  );
}

interface ChartBucket {
  start: number;
  rows: UsageReport["rows"];
}

/**
 * A hand-rolled stacked bar chart: no chart library to ship over a flaky
 * connection, and the data is already bucketed server-side. Tapping a bar
 * selects it and shows the exact numbers below the chart — hover and touch
 * get the same answer.
 */
function StackedChart({
  buckets,
  providers,
  bucketMs,
  value,
  format,
  metric,
}: {
  buckets: ChartBucket[];
  providers: string[];
  bucketMs: number;
  value: (t: UsageReport["totals"]) => number;
  format: (n: number) => string;
  metric: "cost" | "tokens";
}) {
  const [selected, setSelected] = useState<number | null>(null);
  const perBucket = useMemo(
    () => buckets.map((b) => {
      const byProvider = providers.map((p) =>
        b.rows.filter((r) => r.provider === p).reduce((sum, r) => sum + value(r.totals), 0),
      );
      return { start: b.start, byProvider, total: byProvider.reduce((a, b) => a + b, 0) };
    }),
    [buckets, providers, value],
  );
  const max = Math.max(...perBucket.map((b) => b.total), 0);

  const shown = selected != null ? perBucket.find((b) => b.start === selected) : null;
  const shownRows = selected != null ? buckets.find((b) => b.start === selected)?.rows ?? [] : [];

  // Sparse x labels: hourly buckets label every 6th, daily every ~7th, so a
  // phone-width axis never turns into tick soup.
  const labelEvery = bucketMs >= 24 * 3600 * 1000 ? 7 : 6;

  return (
    <div>
      <div className="relative">
        <div className="flex h-36 items-end gap-px md:gap-0.5">
          {perBucket.map((b, i) => (
            <button
              key={b.start}
              onClick={() => setSelected(b.start)}
              aria-label={`${bucketFullLabel(b.start, bucketMs)}: ${format(b.total)} ${metric === "cost" ? "cost" : "tokens"}`}
              className="group relative flex h-full min-w-0 flex-1 cursor-pointer flex-col justify-end outline-none"
            >
              {/* The full-height invisible hit area keeps a thin bar
                  tappable on a phone. */}
              <span className="flex h-full w-full flex-col justify-end">
                {providers.map((p, pi) =>
                  b.byProvider[pi] > 0 ? (
                    <span
                      key={p}
                      style={{
                        height: `${max > 0 ? (b.byProvider[pi] / max) * 100 : 0}%`,
                        background: providerColor(p, pi),
                        opacity: selected == null || selected === b.start ? 1 : 0.45,
                      }}
                      className="w-full transition-opacity"
                    />
                  ) : null,
                )}
                {/* An empty bucket keeps a hairline floor so the axis reads
                    as time, not as missing data. */}
                {b.total === 0 && <span className="bg-muted-foreground/20 h-px w-full" />}
              </span>
              {selected === b.start && (
                <span aria-hidden className="ring-foreground/40 pointer-events-none absolute inset-x-0 bottom-0 h-full rounded-sm ring-1" />
              )}
              <span className="sr-only">{bucketLabel(b.start, bucketMs)}</span>
              {i % labelEvery === (perBucket.length - 1) % labelEvery && (
                <span className="text-muted-foreground/70 absolute inset-x-0 -bottom-5 text-center text-[9px] tabular-nums">
                  {bucketLabel(b.start, bucketMs)}
                </span>
              )}
            </button>
          ))}
        </div>
      </div>
      <div className="text-muted-foreground/70 mt-6 text-center text-[10px]">
        {max > 0 ? `peak ${format(max)} per ${bucketMs >= 86400000 ? "day" : "hour"}` : "no usage in this range"}
      </div>

      {/* The tapped bucket's exact numbers: one surface for hover and for
          touch, with the timestamp and every provider/model line. */}
      {shown && (
        <div className="bg-secondary/40 mt-3 rounded-lg p-3 text-[12px]">
          <p className="mb-1.5 font-medium">{bucketFullLabel(shown.start, bucketMs)}</p>
          {shownRows.length === 0 ? (
            <p className="text-muted-foreground">No usage in this {bucketMs >= 86400000 ? "day" : "hour"}.</p>
          ) : (
            <table className="w-full tabular-nums">
              <tbody>
                {shownRows.map((r) => (
                  <tr key={`${r.provider}/${r.model}`} className="border-border/50 border-t first:border-t-0">
                    <td className="flex items-center gap-1.5 py-1 pr-2">
                      <span
                        aria-hidden
                        className="size-2 shrink-0 rounded-full"
                        style={{ background: providerColor(r.provider, providers.indexOf(r.provider)) }}
                      />
                      <span className="truncate">
                        <span className="capitalize">{r.provider}</span>
                        <span className="text-muted-foreground"> · {r.model || "unknown model"}</span>
                      </span>
                    </td>
                    <td className="text-muted-foreground py-1 text-right">
                      {(r.totals.input / 1e6).toFixed(1)}M in · {(r.totals.output / 1e6).toFixed(1)}M out
                    </td>
                    <td className="py-1 pl-3 text-right font-medium">{format(value(r.totals))}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}
    </div>
  );
}

/** Models ranked by the selected metric, each with a share bar. */
function ModelBreakdown({
  report,
  metric,
  value,
  format,
  providers,
}: {
  report: UsageReport;
  metric: "cost" | "tokens";
  value: (t: UsageReport["totals"]) => number;
  format: (n: number) => string;
  providers: string[];
}) {
  const models = useMemo(() => {
    const byModel = new Map<string, { provider: string; model: string; total: number; totals: UsageReport["totals"] }>();
    for (const row of report.rows) {
      const key = `${row.provider}/${row.model}`;
      const existing = byModel.get(key);
      if (existing) {
        existing.total += value(row.totals);
        existing.totals.cost += row.totals.cost;
        existing.totals.input += row.totals.input;
        existing.totals.output += row.totals.output;
        existing.totals.cacheRead += row.totals.cacheRead;
        existing.totals.cacheWrite += row.totals.cacheWrite;
        existing.totals.unpriced += row.totals.unpriced;
      } else {
        byModel.set(key, { provider: row.provider, model: row.model, total: value(row.totals), totals: { ...row.totals } });
      }
    }
    return [...byModel.values()].sort((a, b) => b.total - a.total);
  }, [report.rows, value]);

  const grand = value(report.totals);
  if (models.length === 0) return null;

  return (
    <section aria-label="Model breakdown">
      <h2 className="mb-2 text-[13px] font-semibold">By model</h2>
      <ul className="flex flex-col gap-2">
        {models.map((m) => {
          const share = grand > 0 ? (m.total / grand) * 100 : 0;
          return (
            <li key={`${m.provider}/${m.model}`} className="flex items-center gap-2.5 text-[12px]">
              <span
                aria-hidden
                className="size-2.5 shrink-0 rounded-full"
                style={{ background: providerColor(m.provider, providers.indexOf(m.provider)) }}
              />
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">{m.model || "Unknown model"}</span>
                <span className="bg-secondary mt-1 block h-1.5 w-full overflow-hidden rounded-full">
                  <span className="block h-full rounded-full" style={{ width: `${share}%`, background: providerColor(m.provider, providers.indexOf(m.provider)) }} />
                </span>
                <span className="text-muted-foreground mt-0.5 block text-[10px]">
                  {formatTokens(m.totals.input)} in · {formatTokens(m.totals.output)} out
                  {m.totals.cacheRead > 0 && ` · ${formatTokens(m.totals.cacheRead)} cached`}
                  {m.totals.unpriced > 0 && metric === "cost" && " · partly unpriced"}
                </span>
              </span>
              <span className="shrink-0 text-right">
                <span className="block font-medium tabular-nums">{format(m.total)}</span>
                <span className="text-muted-foreground block text-[10px] tabular-nums">{Math.round(share)}%</span>
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

/**
 * The Limits view: every provider account's allowance windows, each with its
 * own progress bar, reset countdown and observed age. One provider's refresh
 * failure never blanks another's — every card is independent.
 */
function LimitsView({ quotas, onRefresh }: { quotas: QuotaStatus[]; onRefresh: (instance: string) => Promise<QuotaStatus[]> }) {
  const [refreshing, setRefreshing] = useState<Set<string>>(new Set());
  const [now, setNow] = useState(() => Date.now());

  // A slowly ticking clock keeps the countdowns honest without a render storm.
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(t);
  }, []);

  const refresh = useCallback(
    (instance: string) => {
      setRefreshing((r) => new Set(r).add(instance));
      onRefresh(instance)
        .catch(() => {})
        .finally(() =>
          setRefreshing((r) => {
            const next = new Set(r);
            next.delete(instance);
            return next;
          }),
        );
    },
    [onRefresh],
  );

  if (quotas.length === 0) {
    return <p className="text-muted-foreground mt-6 text-[13px]">No providers are configured.</p>;
  }

  return (
    <div className="mt-2 flex flex-col gap-3">
      {quotas.map((q) => (
        <ProviderLimits key={q.instance} status={q} now={now} refreshing={refreshing.has(q.instance)} onRefresh={() => refresh(q.instance)} />
      ))}
    </div>
  );
}

function ProviderLimits({
  status,
  now,
  refreshing,
  onRefresh,
}: {
  status: QuotaStatus;
  now: number;
  refreshing: boolean;
  onRefresh: () => void;
}) {
  const snap = status.snapshot;
  const observed = snap.checkedAt || status.lastAttempt || 0;
  const stale = status.lastError !== "";

  return (
    <section aria-label={`${status.displayName} usage limits`} className="rounded-xl border p-3">
      <div className="flex items-center gap-2">
        <ActivityIcon aria-hidden className="text-muted-foreground size-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13px] font-semibold">{status.displayName}</p>
          <p className="text-muted-foreground text-[11px]">
            {snap.plan ? `${snap.plan} plan` : "plan unknown"}
            {observed > 0 && ` · observed ${formatAge(observed, now)}`}
          </p>
        </div>
        <Button variant="outline" size="sm" className="h-8 gap-1.5 px-2.5 text-[12px]" onClick={onRefresh} disabled={refreshing}>
          {refreshing ? <Spinner className="size-3.5" /> : <RefreshCwIcon className="size-3.5" />}
          Refresh
        </Button>
      </div>

      {stale && (
        <p className="text-attention-foreground mt-2 flex items-start gap-1.5 rounded-md bg-attention-surface/70 px-2 py-1.5 text-[11px] leading-relaxed">
          <CircleAlertIcon aria-hidden className="mt-0.5 size-3 shrink-0" />
          <span>
            The last refresh failed ({status.lastError}). The figures below are from{" "}
            {formatAge(observed, now)} and may be out of date.
          </span>
        </p>
      )}

      {snap.unavailable === "unsupported" ? (
        <p className="text-muted-foreground mt-3 text-[12px] leading-relaxed">
          This account has no plan limits — an API key or third-party provider, which is billed per
          token rather than capped.
        </p>
      ) : !snap.windows || snap.windows.length === 0 ? (
        <div className="mt-3 flex items-center justify-between gap-3">
          <p className="text-muted-foreground text-[12px] leading-relaxed">
            Not observed yet{observed > 0 && ` — the last attempt was ${formatAge(observed, now)}`}. Refresh asks
            the provider directly, without starting a session or sending a message.
          </p>
        </div>
      ) : (
        <ul className="mt-3 flex flex-col gap-3">
          {snap.windows.map((w) => (
            <QuotaRow key={w.id} window={w} now={now} />
          ))}
        </ul>
      )}
    </section>
  );
}

/** The severity of a window's usage, for the bar's colour. Text always says
 *  the numbers too — colour is a cue, never the only signal. */
function barClass(usedPercent: number): string {
  if (usedPercent >= 95) return "bg-destructive";
  if (usedPercent >= 70) return "bg-attention";
  return "bg-success";
}

function QuotaRow({ window: w, now }: { window: QuotaWindow; now: number }) {
  if (w.kind === "credits") {
    return (
      <li>
        <div className="flex items-baseline justify-between gap-2">
          <p className="text-[12px] font-medium">{w.label}</p>
          <p className="text-[12px] tabular-nums">
            {w.count ?? 0} remaining
            {w.resetsAt && (
              <span className="text-muted-foreground">
                {" "}
                · next expires in {formatCountdown(w.resetsAt, now)}
              </span>
            )}
          </p>
        </div>
        <p className="text-muted-foreground mt-0.5 text-[11px]">
          {w.resetsAt ? new Date(w.resetsAt).toLocaleString() : "No expiry reported"}
        </p>
      </li>
    );
  }

  const used = w.usedPercent;
  const hasReading = used !== undefined;
  const pct = Math.max(0, Math.min(100, used ?? 0));

  return (
    <li>
      <div className="flex items-baseline justify-between gap-2">
        <p className="text-[12px] font-medium">{w.label}</p>
        <p className="text-[12px] tabular-nums">
          {hasReading ? (
            <>
              <span className="font-medium">{pct.toFixed(0)}% used</span>
              <span className="text-muted-foreground"> · {(100 - pct).toFixed(0)}% left</span>
            </>
          ) : (
            <span className="text-muted-foreground">No reading yet</span>
          )}
        </p>
      </div>
      <div
        role="progressbar"
        aria-label={w.label}
        aria-valuenow={hasReading ? Math.round(pct) : undefined}
        aria-valuemin={0}
        aria-valuemax={100}
        className="bg-secondary mt-1.5 h-2 w-full overflow-hidden rounded-full"
      >
        <div className={cn("h-full rounded-full transition-[width]", hasReading && barClass(pct))} style={{ width: `${hasReading ? pct : 0}%` }} />
      </div>
      <p className="text-muted-foreground mt-1 flex items-center gap-1 text-[11px]">
        <ClockIcon aria-hidden className="size-3 shrink-0" />
        {w.resetsAt
          ? `resets in ${formatCountdown(w.resetsAt, now)} · ${new Date(w.resetsAt).toLocaleString()}`
          : "no reset time reported"}
      </p>
    </li>
  );
}