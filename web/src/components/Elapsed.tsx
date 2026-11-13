import { useEffect, useRef } from "react";

import { formatDuration } from "~/rows";

// A live elapsed timer written straight to the DOM: re-rendering a list once a
// second to move a clock would redraw every row in it for one changed string.
// Frozen at `finishedAt - since` once the work is done.
export function Elapsed({
  since,
  finishedAt,
  className,
}: {
  since?: number;
  finishedAt?: number;
  className?: string;
}) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (!since) return;
    const el = ref.current;
    if (!el) return;
    const tick = () => {
      el.textContent = formatDuration((finishedAt ?? Date.now()) - since);
    };
    tick();
    if (finishedAt) return;
    const t = window.setInterval(tick, 1000);
    return () => window.clearInterval(t);
  }, [since, finishedAt]);
  if (!since) return null;
  return <span ref={ref} className={className ?? "tabular-nums"} />;
}
