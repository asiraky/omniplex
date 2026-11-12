import type { ReactNode } from "react";

import { cn } from "~/lib/utils";

export type Tone = "quiet" | "good" | "attention" | "bad";

const MARKER_TONE: Record<Tone, string> = {
  quiet: "text-muted-foreground",
  good: "border-success/40 text-success",
  attention: "border-attention/40 bg-attention-surface text-attention-foreground",
  bad: "border-destructive/40 bg-destructive/10 text-destructive",
};

/** A small pill for the exceptions a row carries; rows with nothing to say have none. */
export function Marker({ tone = "quiet", children }: { tone?: Tone; children: ReactNode }) {
  return (
    <span
      className={cn(
        "shrink-0 rounded-full border px-1.5 py-px text-[10.5px] leading-tight font-medium whitespace-nowrap",
        MARKER_TONE[tone],
      )}
    >
      {children}
    </span>
  );
}
