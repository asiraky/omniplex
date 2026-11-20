import { BotIcon, UserIcon } from "lucide-react";
import { useMemo, type ReactNode } from "react";

import { DiffLines } from "~/components/Diff";
import { cn } from "~/lib/utils";
import { diffStat, proseDiff } from "./agentText";

// Small pieces the agent page's tabs draw the same way.

/** Who wrote a revision: the user by hand, or the agent through an approved card. */
export function WhoChip({ author }: { author: "user" | "agent" }) {
  return author === "agent" ? (
    <span className="text-primary inline-flex items-center gap-1 text-[11.5px]">
      <BotIcon className="size-3" aria-hidden />
      Agent
    </span>
  ) : (
    <span className="text-muted-foreground inline-flex items-center gap-1 text-[11.5px]">
      <UserIcon className="size-3" aria-hidden />
      You
    </span>
  );
}

/** Two versions of a text, framed with a header saying which and how much changed. */
export function DiffBox({
  from,
  to,
  before,
  after,
  layout = "unified",
  className,
}: {
  from: string;
  to: string;
  before: string;
  after: string;
  layout?: "unified" | "split";
  className?: string;
}) {
  const lines = useMemo(() => proseDiff(before, after), [before, after]);
  const stat = useMemo(() => diffStat(before, after), [before, after]);
  return (
    <div data-diff-layout={layout} className={cn("bg-card overflow-hidden rounded-md border", className)}>
      <div className="bg-muted/50 text-muted-foreground flex items-center gap-2 border-b px-3 py-1 font-mono text-[11px]">
        <span>
          {from} → {to}
        </span>
        <span className="ml-auto text-success">+{stat.add}</span>
        <span className="text-destructive">−{stat.del}</span>
      </div>
      <DiffLines lines={lines} layout={layout} wrap />
    </div>
  );
}

/** A stored body shown as it was sent: monospace, wrapped, never reflowed. */
export function BodyText({ children, className }: { children: string; className?: string }) {
  return (
    <pre
      className={cn(
        "bg-card rounded-md border px-3 py-2.5 font-mono text-[12px] leading-relaxed break-words whitespace-pre-wrap",
        className,
      )}
    >
      {children}
    </pre>
  );
}

/** A quiet bordered note under a form, for how something behaves rather than what it is. */
export function InfoBox({ children }: { children: ReactNode }) {
  return (
    <div className="text-muted-foreground rounded-md border px-3 py-2.5 text-[12.5px] leading-relaxed">{children}</div>
  );
}
