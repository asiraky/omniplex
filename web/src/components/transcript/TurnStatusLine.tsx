import { ArchiveIcon, RotateCwIcon } from "lucide-react";
import { useEffect, useState } from "react";

import type { TurnStatus } from "~/protocol";
import { formatDuration } from "~/rows";

/** "Retrying (2/10) — overloaded": everything but the countdown. */
function retryLabel(s: TurnStatus): string {
  const count = s.attempt && s.max ? ` (${s.attempt}/${s.max})` : s.attempt ? ` (attempt ${s.attempt})` : "";
  return `Retrying${count}${s.reason ? ` — ${s.reason}` : ""}`;
}

/** Whole seconds until `at`, rounded up so "1s" shows until the moment itself. */
function countdown(at: number, now: number): string | null {
  const s = Math.ceil((at - now) / 1000);
  if (s <= 0) return null;
  return s < 60 ? `${s}s` : formatDuration(s * 1000);
}

// The clock for the countdown. It ticks only while the retry is still ahead:
// once it is due there is nothing left to count.
function useNow(until: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (Date.now() >= until) return;
    const t = window.setInterval(() => {
      const n = Date.now();
      setNow(n);
      if (n >= until) window.clearInterval(t);
    }, 1000);
    return () => window.clearInterval(t);
  }, [until]);
  return now;
}

/**
 * ", next in 8s". Keyed by the retry time, so each attempt starts from a fresh
 * clock rather than the last tick of the one before it.
 */
function RetryCountdown({ at }: { at: number }) {
  const left = countdown(at, useNow(at));
  if (!left) return null;
  // Hidden from the live region: announcing every second would drown out
  // everything else a screen reader has to say.
  return (
    <span aria-hidden className="tabular-nums">
      , next in {left}
    </span>
  );
}

/**
 * Why a running turn has gone quiet: the harness is retrying a failed model
 * request, or compacting context. One muted line under the working indicator,
 * truncated rather than wrapped so a long reason cannot push the layout about
 * on a phone.
 */
export function TurnStatusLine({ status, running }: { status?: TurnStatus | null; running: boolean }) {
  if (!running || !status?.state) return null;

  const retrying = status.state === "retrying";
  const Icon = retrying ? RotateCwIcon : ArchiveIcon;
  return (
    <div
      role="status"
      aria-live="polite"
      className="text-muted-foreground fade-in flex min-w-0 items-center gap-2 text-[12px]"
    >
      <Icon className="size-3.5 shrink-0 motion-safe:animate-pulse" aria-hidden />
      <span className="min-w-0 truncate">
        {retrying ? retryLabel(status) : "Compacting context…"}
        {retrying && status.retryAt ? <RetryCountdown key={status.retryAt} at={status.retryAt} /> : null}
      </span>
    </div>
  );
}
