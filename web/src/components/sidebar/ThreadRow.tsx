import { CircleAlertIcon, FolderIcon, GitBranchIcon, XIcon } from "lucide-react";

import { HarnessBadge } from "~/components/HarnessBadge";
import { LabelDot, LabelMenu } from "~/components/LabelMenu";
import { Button } from "~/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "~/components/ui/context-menu";
import { DropdownMenu, DropdownMenuTrigger } from "~/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { cn } from "~/lib/utils";
import type { Label, ThreadMeta } from "~/protocol";

const BUSY_PHASES = ["turn", "provisioning", "creating", "cleaning"];
const FAILED_PHASES = ["provision_failed", "cleanup_failed"];

// The server derives attention from the live projection, which knows about
// pending permissions and questions; phase alone does not. The phase sets
// above remain only as a fallback for a server that predates attention.
function working(s: ThreadMeta) {
  return s.attention ? s.attention === "working" : BUSY_PHASES.includes(s.phase);
}
function needsInput(s: ThreadMeta) {
  return s.attention === "needs_permission" || s.attention === "needs_answer";
}
// Nothing is waiting on the reader, but jobs are still running beside the
// conversation. Steady, not pulsing: nothing to look at yet.
function background(s: ThreadMeta) {
  return s.attention === "background";
}
function failed(s: ThreadMeta) {
  return s.attention ? s.attention === "failed" : FAILED_PHASES.includes(s.phase);
}
// The log has moved past what anyone has read, on any paired device: "the
// agent finished while I was away", which nothing else in the row can say.
// lastViewedSeq is absent on a server that predates it; treating that as
// seq 0 would light every row, so an absent cursor reads as all-read.
function unread(s: ThreadMeta) {
  return s.lastViewedSeq !== undefined && s.headSeq > s.lastViewedSeq;
}
// The row is asking for someone's attention right now, one way or another.
// Quiet rows — read, idle, nobody waiting — visually recede below these.
function loud(s: ThreadMeta) {
  return working(s) || needsInput(s) || failed(s) || background(s);
}

function ago(ms: number) {
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

/** The title line: the title, whatever the thread is doing, and when. */
function TitleLine({
  s,
  label,
  labels,
  showUnread,
}: {
  s: ThreadMeta;
  label: Label | undefined;
  labels: Label[];
  showUnread: boolean;
}) {
  return (
    <span
      className={cn(
        "flex items-center gap-1.5",
        // Room for the always-visible touch controls: the X, and
        // the label tag beside it once labels exist. On desktop the
        // controls only exist on hover, so the line runs full width
        // until then — but it has to yield when they arrive. The
        // fading timestamp alone covers one control; a second one
        // would sit on top of the title, so with labels in play the
        // hovered line yields as well — but only as far as the
        // label dot's left edge (pr-12), not the pair's full 64px
        // box. The buttons are mostly hit area: reserving all of it
        // leaves a canyon between the truncated title and the X.
        //
        // The open label menu is the third case: Radix moves focus
        // into a portal, so once the pointer leaves the row neither
        // hover nor focus-within holds, yet the trigger stays lit.
        // `has` reads it off the trigger's own aria-expanded, which
        // — unlike data-state — no tooltip on the same element can
        // claim. Deliberately not transitioned: an animated padding
        // hands the tag ~150ms sitting on the title, which is the
        // bug in miniature. The line yields first, then it fades in.
        // A filed thread shows its dot at all times, so on
        // desktop the line has to yield at all times too —
        // hover-only reservation would leave the title running
        // under a dot that is already there.
        label
          ? "pr-16 md:pr-12"
          : labels.length > 0
            ? "pr-16 md:pr-0 md:group-hover:pr-12 md:group-focus-within:pr-12 md:group-has-[[aria-expanded=true]]:pr-12"
            : "pr-8 md:pr-0",
      )}
    >
      <span className="min-w-0 truncate text-[13px]">{s.title || "Untitled"}</span>
      {!!s.scheduledCount && (
        <span
          className="shrink-0 text-xs text-muted-foreground"
          title={`${s.scheduledCount} scheduled messages`}
          aria-label={`${s.scheduledCount} scheduled messages`}
        >
          ◷ {s.scheduledCount}
        </span>
      )}
      {working(s) && (
        <span
          role="status"
          aria-label="Working"
          className="bg-primary size-1.5 shrink-0 animate-pulse rounded-full motion-reduce:animate-none"
        />
      )}
      {background(s) && !failed(s) && (
        <span
          role="status"
          aria-label="Jobs running"
          className="bg-primary/60 size-1.5 shrink-0 rounded-full"
        />
      )}
      {needsInput(s) && (
        <span
          role="status"
          aria-label="Waiting for your input"
          className="bg-attention size-1.5 shrink-0 animate-pulse rounded-full motion-reduce:animate-none"
        />
      )}
      {failed(s) && (
        <CircleAlertIcon
          aria-label="Needs attention"
          className="text-destructive size-3 shrink-0"
        />
      )}
      {/* Completed-and-unseen, in its own colour: not "in motion"
         (primary), not "act now" (attention) — done, waiting to
         be read. Steady on purpose; nothing is happening. */}
      {showUnread && (
        <span
          role="status"
          aria-label="Finished since you last looked"
          className="bg-success size-1.5 shrink-0 rounded-full"
        />
      )}
      <span className="text-muted-foreground ml-auto shrink-0 font-mono text-[10px] transition-opacity md:group-hover:opacity-0 md:group-focus-within:opacity-0">
        {ago(s.updatedAt)}
      </span>
    </span>
  );
}

/** The line under the title: where the thread works, and on what. */
function DetailLine({
  s,
  showProject,
  projectName,
  accentOf,
}: {
  s: ThreadMeta;
  showProject: boolean;
  projectName: (id?: string) => string | undefined;
  accentOf: (harness: string) => string | undefined;
}) {
  return (
    <span className="text-muted-foreground mt-1 flex min-w-0 items-center gap-1 font-mono text-[10px]">
      {/* Under a group header the project is already named a few
         pixels up, so the line gives the space to the branch —
         the thing that actually tells two threads in one project
         apart. Ungrouped, the project comes back: there is no
         header then, and the row is the only thing that says it. */}
      {showProject ? (
        <>
          <FolderIcon aria-hidden className="size-3 shrink-0" />
          {/* With a branch alongside it the project keeps its natural
             width up to half the line and the branch takes what is
             left, so a long branch can no longer shrink a short
             project name to a letter and an ellipsis. With no branch
             there is nothing to share with, and the cap would only
             truncate a name that fits. */}
          <span className={cn("truncate", s.branch ? "max-w-[50%] shrink-0" : "min-w-0")}>
            {projectName(s.projectId) ?? s.cwd.split("/").slice(-2).join("/")}
          </span>
          {s.branch && (
            <>
              <GitBranchIcon aria-hidden className="ml-1 size-3 shrink-0" />
              <span className="min-w-0 flex-1 truncate">{s.branch}</span>
            </>
          )}
        </>
      ) : s.branch ? (
        <>
          <GitBranchIcon aria-hidden className="size-3 shrink-0" />
          <span className="min-w-0 flex-1 truncate">{s.branch}</span>
        </>
      ) : (
        // No branch to show and no project to repeat: the checkout
        // is the only thing left that distinguishes the row.
        <>
          <FolderIcon aria-hidden className="size-3 shrink-0" />
          <span className="min-w-0 flex-1 truncate">{s.cwd.split("/").slice(-2).join("/")}</span>
        </>
      )}
      <span className="ml-auto flex shrink-0 items-center pl-1.5">
        <HarnessBadge harness={s.harness} accent={accentOf(s.harness)} className="size-3.5" />
      </span>
    </span>
  );
}

/** The row's label dot, and the menu that files the thread under one. */
function LabelControl({
  s,
  label,
  labels,
  onSetLabel,
  onManageLabels,
}: {
  s: ThreadMeta;
  label: Label | undefined;
  labels: Label[];
  onSetLabel: (threadId: string, labelId: string) => void;
  onManageLabels: () => void;
}) {
  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              aria-label={
                label
                  ? `Labelled ${label.name} — change label`
                  : `Label thread ${s.title || "Untitled"}`
              }
              // Sits one control-width left of the X and reveals
              // the same way, so the pair reads as one action rail.
              // It also stays up while its menu is: keyed off
              // aria-expanded, because the tooltip wrapped around
              // this same element wins the data-state attribute and
              // reports "closed" with the menu plainly open.
              //
              // A filed thread keeps its dot on screen at all
              // times — the dot *is* the label now, and it is the
              // only place the filing shows. An unfiled one keeps
              // the old hover-in behaviour, so an untouched list
              // stays as quiet as it was.
              className={cn(
                "absolute top-0.5 right-8 size-8 shrink-0 after:absolute after:-inset-1.5 after:content-[''] md:size-8 md:after:hidden md:aria-expanded:opacity-100",
                !label && "md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100",
              )}
            >
              {label ? (
                <LabelDot color={label.color} className="size-2.5 shrink-0 rounded-full" />
              ) : (
                // Unfiled reads as an empty socket rather than a
                // grey label: a ring, not a filled dot.
                <span
                  aria-hidden
                  className="border-muted-foreground/60 size-2.5 shrink-0 rounded-full border"
                />
              )}
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        {/* The name lives here and nowhere else, which is the
           trade the dot makes: no truncated text in the row, one
           hover (or one tap, on the menu) to find out. */}
        <TooltipContent>{label ? label.name : "Label thread"}</TooltipContent>
      </Tooltip>
      <LabelMenu
        labels={labels}
        current={s.labelId}
        onSelect={(labelId) => onSetLabel(s.id, labelId)}
        onManage={onManageLabels}
      />
    </DropdownMenu>
  );
}

function DeleteControl({ s, onDelete }: { s: ThreadMeta; onDelete: (s: ThreadMeta) => void }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Delete thread ${s.title || "Untitled"}`}
          onClick={() => onDelete(s)}
          // Aligned to the provider logo's column below it: the logo
          // (14px, full-bleed) is centred 17px from the row's edge,
          // and the X's lucide glyph carries ~1.5px of optical padding
          // inside its 16px box — right-px puts the visible strokes on
          // that same centre line.
          // The visible square stays 32px so it keeps that alignment
          // at every size; `after` grows the hit area to 44px without
          // moving anything, which a larger button could not do.
          className="hover:text-destructive absolute top-0.5 right-px size-8 shrink-0 after:absolute after:-inset-1.5 after:content-[''] md:size-8 md:opacity-0 md:after:hidden md:group-hover:opacity-100 md:focus-visible:opacity-100"
        >
          <XIcon />
        </Button>
      </TooltipTrigger>
      <TooltipContent>Delete thread</TooltipContent>
    </Tooltip>
  );
}

// The row carries its actions as overlaid buttons rather than click handlers
// on the container — a button cannot legally nest inside another button. The
// delete X (and, with labels defined, the label tag beside it) overlays the
// timestamp's corner instead of owning a column of its own, so an un-hovered
// row has no phantom right margin; on hover (desktop) the timestamp yields.
export function ThreadRow({
  s,
  showProject,
  active,
  leaving,
  going,
  labels,
  accentOf,
  projectName,
  onSelect,
  onSetLabel,
  onManageLabels,
  onSetUnread,
  onDelete,
}: {
  s: ThreadMeta;
  showProject: boolean;
  active: boolean;
  /** Already out of the list, and folding away. */
  leaving: boolean;
  /** Being deleted, and still in the list. */
  going: boolean;
  labels: Label[];
  accentOf: (harness: string) => string | undefined;
  projectName: (id?: string) => string | undefined;
  onSelect: (id: string) => void;
  onSetLabel: (threadId: string, labelId: string) => void;
  onManageLabels: () => void;
  onSetUnread: (threadId: string, unread: boolean) => void;
  onDelete: (s: ThreadMeta) => void;
}) {
  // The unread dot yields to every live indicator — a row that is working,
  // waiting or failed already says something stronger — and to the active
  // row, which is by definition being looked at.
  const showUnread = unread(s) && !active && !loud(s);
  // A row that is read, idle and not selected is waiting on nobody: it
  // recedes, so the rows that need eyes stand out by contrast instead of
  // by yet more chrome.
  const recede = !active && !loud(s) && !unread(s);
  // Undefined for unlabelled, and for a label another device has just
  // deleted — the assignment broadcast can land after the deletion one.
  const label = labels.find((l) => l.id === s.labelId);
  return (
    // The row leaves from wherever it stands: it fades and slides out
    // while its own height folds shut under it, so the rows below close
    // the gap in the same motion instead of snapping up. The height is
    // the `1fr`→`0fr` grid track, which is the one way to transition to
    // a content-sized height the row never had to declare.
    <div
      inert={leaving}
      className={cn(
        "grid transition-[grid-template-rows,opacity,transform,margin] duration-[260ms] ease-out motion-reduce:transition-none",
        leaving
          ? "mb-0 grid-rows-[0fr] -translate-x-2 scale-[0.98] opacity-0"
          : "mb-0.5 grid-rows-[1fr]",
      )}
    >
      {/* Right-click (long-press, on touch) for the row's quiet
         actions. Only read-state today: everything else the row does
         already has a control of its own. */}
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            className={cn(
              // min-w-0: a grid item's automatic minimum size is its
              // min-content width, and a `truncate`d line is `nowrap` — so
              // its min-content is the whole untruncated string. Left at
              // `auto` the row grows to fit the longest title and is clipped
              // by the scroller instead of ever reaching the ellipsis.
              "group relative min-w-0 rounded-lg transition-colors",
              leaving && "overflow-hidden",
              active
                ? "bg-sidebar-accent text-sidebar-accent-foreground"
                : "hover:bg-sidebar-accent/60",
              // Receded rows come back to full strength under the pointer
              // (or focus), so dimming never costs legibility at the moment
              // of use.
              recede && "opacity-60 hover:opacity-100 focus-within:opacity-100",
              // Already on its way out: it shows what it is doing (the busy
              // dot below) but no longer takes clicks.
              going && "pointer-events-none opacity-60",
            )}
          >
            <button
              type="button"
              onClick={() => onSelect(s.id)}
              aria-current={active ? "true" : undefined}
              className="focus-visible:ring-ring block w-full min-w-0 cursor-pointer rounded-lg px-2.5 py-2 text-left outline-none focus-visible:ring-2"
            >
              {/* Two matched lines: text on the left, a small mark on the
                  right — timestamp above, provider logo below. */}
              <TitleLine s={s} label={label} labels={labels} showUnread={showUnread} />
              <DetailLine
                s={s}
                showProject={showProject}
                projectName={projectName}
                accentOf={accentOf}
              />
            </button>

            {labels.length > 0 && (
              <LabelControl
                s={s}
                label={label}
                labels={labels}
                onSetLabel={onSetLabel}
                onManageLabels={onManageLabels}
              />
            )}

            <DeleteControl s={s} onDelete={onDelete} />
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          {unread(s) ? (
            <ContextMenuItem onSelect={() => onSetUnread(s.id, false)}>Mark read</ContextMenuItem>
          ) : (
            // A thread whose log is empty has nothing to be unread about.
            <ContextMenuItem disabled={s.headSeq === 0} onSelect={() => onSetUnread(s.id, true)}>
              Mark unread
            </ContextMenuItem>
          )}
        </ContextMenuContent>
      </ContextMenu>
    </div>
  );
}
