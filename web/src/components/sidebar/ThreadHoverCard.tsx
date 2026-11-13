import { FolderIcon, GitBranchIcon } from "lucide-react";
import { Tooltip as TooltipPrimitive } from "radix-ui";
import type { ReactNode } from "react";

import { HarnessBadge } from "~/components/HarnessBadge";
import { cn } from "~/lib/utils";
import type { ThreadMeta } from "~/protocol";
import { unread } from "~/threadStatus";
import { useCanHover } from "~/useMediaQuery";
import { useNow } from "~/useNow";

/** Long enough that sweeping the pointer down the list opens nothing. */
const OPEN_DELAY_MS = 500;

type Tone = "busy" | "waiting" | "failed" | "quiet";

/**
 * What the thread is doing, in words. The row says it with a ring or a badge;
 * the card has room to say which kind of waiting it is. Absent when the server
 * sends no attention signal, rather than guessing from the phase.
 */
export function threadStatus(s: ThreadMeta): { label: string; tone: Tone } | null {
  switch (s.attention) {
    case "working":
      return { label: "Working", tone: "busy" };
    case "background":
      return { label: "Background task running", tone: "busy" };
    case "needs_permission":
      return { label: "Waiting for permission", tone: "waiting" };
    case "needs_answer":
      return { label: "Waiting for your answer", tone: "waiting" };
    case "needs_prompt":
      return s.turnFailed
        ? { label: "Last turn failed", tone: "failed" }
        : { label: "Finished", tone: "quiet" };
    case "failed":
      return { label: "Workspace failed", tone: "failed" };
    case "closed":
      return { label: "Closed", tone: "quiet" };
    default:
      return null;
  }
}

function ago(ms: number, now: number) {
  const s = Math.max(0, Math.floor((now - ms) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

function Line({ icon, children }: { icon: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-start gap-2">
      <span className="flex h-4 shrink-0 items-center">{icon}</span>
      <span className="min-w-0 break-all">{children}</span>
    </div>
  );
}

/**
 * The card itself. Everything on it is already in the sidebar's summary of
 * the thread: hovering never asks the server for anything.
 */
function Card({
  s,
  projectName,
  accentOf,
}: {
  s: ThreadMeta;
  projectName: (id?: string) => string | undefined;
  accentOf: (harness: string) => string | undefined;
}) {
  const now = useNow();
  const status = threadStatus(s);
  const project = projectName(s.projectId);
  // The row truncates the checkout to its last two segments; the card has the
  // room for the whole path, which is what tells two clones apart.
  const where = project ?? s.cwd;
  const fresh = unread(s) && status?.tone !== "busy";
  return (
    <div className="flex flex-col gap-2">
      <div className="text-[13px] leading-snug font-medium [overflow-wrap:anywhere]">
        {s.title || "Untitled"}
      </div>
      {status && (
        <div className="flex items-center gap-2 text-xs">
          <span
            aria-hidden
            className={cn(
              "size-2 shrink-0 rounded-full",
              status.tone === "busy" && "bg-primary motion-safe:animate-pulse",
              status.tone === "waiting" && "bg-attention",
              status.tone === "failed" && "bg-destructive",
              status.tone === "quiet" && "bg-muted-foreground/50",
            )}
          />
          <span>{status.label}</span>
          {fresh && <span className="text-muted-foreground">· new since you last looked</span>}
        </div>
      )}
      <div className="text-muted-foreground grid gap-1 font-mono text-[11px]">
        <Line icon={<FolderIcon aria-hidden className="size-3" />}>{where}</Line>
        {s.branch && (
          <Line icon={<GitBranchIcon aria-hidden className="size-3" />}>{s.branch}</Line>
        )}
        <Line
          icon={
            <HarnessBadge harness={s.harness} accent={accentOf(s.harness)} className="size-3" />
          }
        >
          {[s.model || s.harness, s.mode].filter(Boolean).join(" · ")}
        </Line>
      </div>
      <div className="text-muted-foreground flex flex-wrap gap-x-2 text-[11px]">
        <span>Updated {ago(s.updatedAt, now)}</span>
        {!!s.scheduledCount && (
          <span>
            · {s.scheduledCount} scheduled message
            {s.scheduledCount === 1 ? "" : "s"}
          </span>
        )}
      </div>
    </div>
  );
}

/**
 * The preview a sidebar row opens when a mouse rests on it: the whole title,
 * what the thread is doing, and where it works. Desktop only — a touch screen
 * has no hover, so on one the card never renders, and a tap still just opens
 * the thread.
 *
 * Built on the tooltip primitive rather than a hover card: a tooltip closes the
 * moment the row is pressed and does not reopen from the focus that press
 * gives the row, so clicking a row never leaves its own card over the
 * transcript it opened.
 */
export function ThreadHoverCard({
  s,
  projectName,
  accentOf,
  disabled,
  children,
}: {
  s: ThreadMeta;
  projectName: (id?: string) => string | undefined;
  accentOf: (harness: string) => string | undefined;
  /** No card at all: the row is being renamed, reordered, or dragged. */
  disabled?: boolean;
  /** The row's button: the card's trigger. */
  children: ReactNode;
}) {
  if (disabled) return children;
  return (
    <TooltipPrimitive.Root delayDuration={OPEN_DELAY_MS} disableHoverableContent>
      <TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger>
      <CardContent s={s} projectName={projectName} accentOf={accentOf} />
    </TooltipPrimitive.Root>
  );
}

function CardContent(props: {
  s: ThreadMeta;
  projectName: (id?: string) => string | undefined;
  accentOf: (harness: string) => string | undefined;
}) {
  // The portal renders its children only while the card is open, so the
  // pointer check below costs a list of rows nothing until one is hovered.
  return (
    <TooltipPrimitive.Portal>
      <HoverOnly>
        <TooltipPrimitive.Content
          data-slot="thread-hover-card"
          side="right"
          align="start"
          sideOffset={8}
          collisionPadding={8}
          className="bg-popover text-popover-foreground animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 z-50 w-72 origin-(--radix-tooltip-content-transform-origin) rounded-lg border p-3 shadow-md"
        >
          <Card {...props} />
        </TooltipPrimitive.Content>
      </HoverOnly>
    </TooltipPrimitive.Portal>
  );
}

/**
 * Nothing without a pointer that hovers. Not rendering the card on touch,
 * rather than hiding it, matters for the reason the shared tooltip gives: an
 * invisible card is still a layer, and it would swallow the Escape meant for
 * the sheet under it.
 */
function HoverOnly({ children }: { children: ReactNode }) {
  return useCanHover() ? children : null;
}
