import { FolderGit2Icon, FolderIcon, GitBranchIcon } from "lucide-react";
import { Tooltip as TooltipPrimitive } from "radix-ui";
import { useRef, useState, type ReactNode } from "react";

import { HarnessBadge } from "~/components/HarnessBadge";
import { fmtAgo } from "~/lib/format";
import { cn } from "~/lib/utils";
import type { ThreadMeta } from "~/protocol";
import { attentionLabel } from "~/threadStatus";
import { CAN_HOVER, DESKTOP } from "~/useMediaQuery";
import { useNow } from "~/useNow";

/** Long enough that sweeping the pointer down the list opens nothing. */
const OPEN_DELAY_MS = 500;

type CardProps = {
  s: ThreadMeta;
  /** The row's own `new` badge: the card says it in words, never on its own reckoning. */
  isNew: boolean;
  projectName: (id?: string) => string | undefined;
  accentOf: (harness: string) => string | undefined;
};

/**
 * Only with a mouse, and only beside a docked sidebar. Touch has no hover;
 * below md the sidebar is a sheet that fills the screen, so a card beside it
 * would open off the edge, and the row there is wide enough to say most of
 * what the card would. Read when the card is about to open rather than held in
 * a hook, so a list of rows keeps no media listeners for it.
 */
function previewable() {
  return window.matchMedia(CAN_HOVER).matches && window.matchMedia(DESKTOP).matches;
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
function Card({ s, isNew, projectName, accentOf }: CardProps) {
  const now = useNow();
  const status = attentionLabel(s);
  const project = projectName(s.projectId);
  const ago = fmtAgo(s.updatedAt, now);
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
          {isNew && <span className="text-muted-foreground">· new since you last looked</span>}
        </div>
      )}
      <div className="text-muted-foreground grid gap-1 font-mono text-[11px]">
        {project && <Line icon={<FolderIcon aria-hidden className="size-3" />}>{project}</Line>}
        {/* The row shortens the checkout to two segments, or drops it for the
            branch; the whole path is what tells two worktrees of one project
            apart. */}
        <Line icon={<FolderGit2Icon aria-hidden className="size-3" />}>{s.cwd}</Line>
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
        <span>Updated {ago === "now" ? "just now" : `${ago} ago`}</span>
        {!!s.scheduledCount && (
          <span>
            · {s.scheduledCount} scheduled message{s.scheduledCount === 1 ? "" : "s"}
          </span>
        )}
      </div>
    </div>
  );
}

/**
 * The preview a sidebar row opens when a mouse rests on it: the whole title,
 * what the thread is doing, and where it works. Desktop only (see
 * `previewable`); a tap still just opens the thread.
 *
 * Built on the tooltip primitive rather than a hover card: a tooltip closes the
 * moment the row is pressed and does not reopen from the focus that press
 * gives the row. It opens only while the pointer is actually on the row, so
 * focus arriving any other way — the window regaining focus, a menu handing
 * it back — does not put a card over the transcript.
 *
 * Its own provider, so every row waits out the delay: the app's shared one
 * would skip it within moments of any tooltip closing, and the card would
 * flash open for every row the pointer crossed.
 */
export function ThreadHoverCard({
  disabled,
  children,
  ...card
}: CardProps & {
  /** No card at all: the row is in reorder mode, something to move. */
  disabled?: boolean;
  /** The row's button: the card's trigger. */
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const pointerOn = useRef(false);
  if (disabled) return children;
  return (
    <TooltipPrimitive.Provider
      delayDuration={OPEN_DELAY_MS}
      skipDelayDuration={0}
      disableHoverableContent
    >
      <TooltipPrimitive.Root
        open={open}
        onOpenChange={(next) => setOpen(next && pointerOn.current && previewable())}
      >
        <TooltipPrimitive.Trigger
          asChild
          onPointerMove={(e) => {
            if (e.pointerType !== "touch") pointerOn.current = true;
          }}
          onPointerLeave={() => {
            pointerOn.current = false;
          }}
        >
          {children}
        </TooltipPrimitive.Trigger>
        <TooltipPrimitive.Portal>
          <TooltipPrimitive.Content
            data-slot="thread-hover-card"
            side="right"
            align="start"
            sideOffset={8}
            collisionPadding={8}
            className="bg-popover text-popover-foreground animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 z-50 w-72 max-w-(--radix-tooltip-content-available-width) origin-(--radix-tooltip-content-transform-origin) rounded-lg border p-3 shadow-md"
          >
            <Card {...card} />
          </TooltipPrimitive.Content>
        </TooltipPrimitive.Portal>
      </TooltipPrimitive.Root>
    </TooltipPrimitive.Provider>
  );
}
