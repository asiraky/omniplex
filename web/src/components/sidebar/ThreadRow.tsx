import {
  FolderIcon,
  GitBranchIcon,
  GripVerticalIcon,
  PencilIcon,
  PinIcon,
  PinOffIcon,
  XIcon,
} from "lucide-react";
import { useRef, useState } from "react";

import { HarnessBadge } from "~/components/HarnessBadge";
import { TitleEditor } from "~/components/TitleEditor";
import { Button } from "~/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "~/components/ui/context-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { cn } from "~/lib/utils";
import type { ThreadMeta } from "~/protocol";
import { rowStatus, unread, type RowStatus } from "~/threadStatus";

import type { RowDrag } from "./SortableRows";

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
  isNew,
  reordering,
}: {
  s: ThreadMeta;
  /** Unread and stopped: the title steps up alongside the badge. */
  isNew: boolean;
  /** The row's controls are put away in reorder mode, so the line keeps its
      full width. */
  reordering: boolean;
}) {
  return (
    <span
      className={cn(
        "flex items-center gap-1.5",
        // Room for the always-visible touch controls: the X and the pin
        // beside it. On desktop an unpinned row's controls only exist on
        // hover, so the line runs full width until then — but it has to
        // yield when they arrive. The fading timestamp alone covers one
        // control; the pin would sit on top of the title, so the hovered
        // line yields as well. Hover also brings the rename pencil, one
        // control-width further left, so the hovered line yields to the
        // pencil's glyph (pr-18) rather than the trio's full 96px box: the
        // buttons are mostly hit area, and reserving all of it leaves a
        // canyon between the truncated title and the X. Deliberately not
        // transitioned: an animated padding hands the pin ~150ms sitting on
        // the title. The line yields first, then the controls fade in.
        //
        // A pinned thread shows its pin at all times, so on desktop the
        // line has to yield at all times too — but only as far as the pin
        // glyph's left edge (pr-12), with the timestamp still showing.
        reordering
          ? null
          : s.pinned
            ? "pr-16 md:pr-12 md:group-hover:pr-18 md:group-focus-within:pr-18"
            : "pr-16 md:pr-0 md:group-hover:pr-18 md:group-focus-within:pr-18",
      )}
    >
      <span className={cn("min-w-0 truncate text-[13px]", isNew && "font-semibold")}>
        {s.title || "Untitled"}
      </span>
      {!!s.scheduledCount && (
        <span
          className="shrink-0 text-xs text-muted-foreground"
          title={`${s.scheduledCount} scheduled messages`}
          aria-label={`${s.scheduledCount} scheduled messages`}
        >
          ◷ {s.scheduledCount}
        </span>
      )}
      <span className="text-muted-foreground ml-auto shrink-0 font-mono text-[10px] transition-opacity md:group-hover:opacity-0 md:group-focus-within:opacity-0">
        {ago(s.updatedAt)}
      </span>
    </span>
  );
}

type Badge = "new" | "failed";

/** The one loud thing a row can carry. Lowercase on purpose: it is a tag, not
    a heading. */
function StatusBadge({ badge }: { badge: Badge }) {
  return (
    <span
      role="status"
      aria-label={badge === "new" ? "New since you last looked" : "Workspace failed"}
      className={cn(
        "rounded-full px-1.5 font-sans text-[10px] leading-[15px] font-semibold",
        // Dark mode's accents are light, so the 10px label goes dark on them
        // to stay readable.
        badge === "new" ? "bg-primary text-primary-foreground" : "bg-destructive text-white",
        "dark:text-background",
      )}
    >
      {badge}
    </span>
  );
}

/** The line under the title: where the thread works, and on what. */
function DetailLine({
  s,
  status,
  badge,
  showProject,
  projectName,
  accentOf,
}: {
  s: ThreadMeta;
  status: RowStatus;
  badge: Badge | null;
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
      {/* The row's status column: always the right edge of this line, so
         it lines up down the list, and well clear of the pin on the line
         above. */}
      <span className="ml-auto flex shrink-0 items-center gap-1.5 pl-1.5">
        {badge && <StatusBadge badge={badge} />}
        <span className="relative flex">
          <HarnessBadge harness={s.harness} accent={accentOf(s.harness)} className="size-3.5" />
          {/* Busy is something you can find if you look for it, and
             nothing more: a thin ring turning round the harness's own
             mark, no colour of its own to compete with the badge. */}
          {status === "busy" && (
            <span
              role="status"
              aria-label="Working"
              className="border-muted-foreground/25 border-t-muted-foreground pointer-events-none absolute -inset-[3px] animate-spin rounded-full border-[1.5px] [animation-duration:1.6s] motion-reduce:animate-none"
            />
          )}
        </span>
      </span>
    </span>
  );
}

/**
 * The pin toggle, one control-width left of the X. A pinned thread keeps it on
 * screen at all times, since it is the row's only sign of being pinned beyond
 * the section it sits in. An unpinned one reveals it on hover like the X, so
 * an untouched list stays quiet on desktop; a phone has no hover, so there it
 * is always showing.
 */
function PinControl({
  s,
  onSetPinned,
}: {
  s: ThreadMeta;
  onSetPinned: (threadId: string, pinned: boolean) => void;
}) {
  const pinned = !!s.pinned;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`${pinned ? "Unpin" : "Pin"} thread ${s.title || "Untitled"}`}
          onClick={() => onSetPinned(s.id, !pinned)}
          // The same 32px square as the X, grown to 44px on touch the same
          // way, so the pair reads as one action rail.
          className={cn(
            "group/pin absolute top-0.5 right-8 size-8 shrink-0 after:absolute after:-inset-1.5 after:content-[''] md:size-8 md:after:hidden",
            pinned
              ? "text-foreground"
              : "text-muted-foreground hover:text-foreground md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100",
          )}
        >
          {/* 14px, a step under the X: on desktop a pinned row's title
              stops just short of it. Pointed at, a pin turns into what the
              click will do. */}
          {pinned ? (
            <>
              <PinIcon className="size-3.5 fill-current group-hover/pin:hidden" />
              <PinOffIcon className="hidden size-3.5 group-hover/pin:block" />
            </>
          ) : (
            <PinIcon className="size-3.5" />
          )}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{pinned ? "Unpin" : "Pin"}</TooltipContent>
    </Tooltip>
  );
}

/**
 * The pencil that turns the title into a field. Desktop only, on hover like
 * the X: a third always-visible control on a phone row would cost the title
 * too much room, so touch reaches rename through the long-press menu, or the
 * pencil in the thread header.
 */
function RenameControl({ s, onRename }: { s: ThreadMeta; onRename: () => void }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={`Rename thread ${s.title || "Untitled"}`}
          onClick={onRename}
          // Left of the pin, which is always in the rail.
          className="text-muted-foreground/70 hover:text-foreground absolute top-0.5 right-16 hidden size-8 shrink-0 hover:bg-transparent md:size-8 md:inline-flex md:opacity-0 md:group-hover:opacity-100 md:focus-visible:opacity-100 dark:hover:bg-transparent"
        >
          {/* Smaller and fainter than the X beside it: renaming is the
              quieter of the two, and the title it edits is 13px. */}
          <PencilIcon className="size-3" strokeWidth={1.75} />
        </Button>
      </TooltipTrigger>
      <TooltipContent>Rename thread</TooltipContent>
    </Tooltip>
  );
}

/**
 * The grip a row is dragged by in reorder mode, down its left edge inside the
 * row. 44px wide and the row's full height, so a thumb finds it without
 * aiming; the row's other controls are put away while it is up.
 *
 * touch-none gives the gesture to the drag instead of the scroller from the
 * first pixel.
 */
function DragHandle({ s, drag }: { s: ThreadMeta; drag: RowDrag | undefined }) {
  return (
    <button
      type="button"
      aria-label={`Move thread ${s.title || "Untitled"}`}
      disabled={!drag}
      {...drag?.handle}
      className="text-muted-foreground hover:text-foreground focus-visible:ring-ring absolute inset-y-0 left-0 flex w-11 cursor-grab touch-none items-center justify-center rounded-lg outline-none select-none [-webkit-touch-callout:none] focus-visible:ring-2 active:cursor-grabbing disabled:cursor-default disabled:opacity-40"
    >
      <GripVerticalIcon aria-hidden className="size-3.5" />
    </button>
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
// delete X and the pin beside it overlay the timestamp's corner instead of owning a column of its own, so an un-hovered
// row has no phantom right margin; on hover (desktop) the timestamp yields.
export function ThreadRow({
  s,
  showProject,
  active,
  leaving,
  going,
  reordering,
  drag,
  accentOf,
  projectName,
  onSelect,
  onSetPinned,
  onSetUnread,
  onRename,
  onDelete,
}: {
  s: ThreadMeta;
  showProject: boolean;
  active: boolean;
  /** Already out of the list, and folding away. */
  leaving: boolean;
  /** Being deleted, and still in the list. */
  going: boolean;
  /** The list is in reorder mode: a grip instead of the controls, and a tap
      that opens nothing — the row is something to move, not to open. */
  reordering: boolean;
  /** How the row is dragged; absent while the list is held still. */
  drag: RowDrag | undefined;
  accentOf: (harness: string) => string | undefined;
  projectName: (id?: string) => string | undefined;
  onSelect: (id: string) => void;
  onSetPinned: (threadId: string, pinned: boolean) => void;
  onSetUnread: (threadId: string, unread: boolean) => void;
  onRename: (threadId: string, title: string) => void;
  onDelete: (s: ThreadMeta) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  // Picking Rename from the context menu closes it, and a closing menu hands
  // focus back to wherever it came from, which would blur the field it just
  // opened. This says the close is ours, so the menu leaves focus alone.
  const renameChosen = useRef(false);
  const status = rowStatus(s);
  // The active row is being read, so it has nothing new to announce.
  const badge: Badge | null =
    status === "failed" ? "failed" : status === "new" && !active ? "new" : null;
  // Only a badged row is loud. Everything else — read, idle, or busy with
  // work nobody has to watch — recedes, so the badges stand out by contrast.
  const recede = !active && badge === null;
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
         actions: rename, which on touch has no control of its own,
         and read-state. */}
      {/* Not modal: a modal menu traps focus until it has finished
         closing, so the rename field it opens would have its focus pulled
         back and lose it. It has to take focus inside the tap itself, too,
         or iOS will not raise the keyboard for it. */}
      <ContextMenu modal={false}>
        {/* While renaming, a right-click in the field is the browser's
           own menu: cut, copy, paste. */}
        {/* In reorder mode the long-press belongs to nobody: the grip is
           the gesture, and a menu rising mid-drag would steal it. */}
        <ContextMenuTrigger asChild disabled={renaming || reordering}>
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
              // ring below) but no longer takes clicks.
              going && "pointer-events-none opacity-60",
            )}
          >
            {renaming ? (
              // The same box as the button below, so the row keeps its
              // shape: the title line becomes the field, the detail line
              // stays put. The row's controls step aside while it is open
              // and the field takes the full width.
              <div className="min-w-0 px-2.5 py-2">
                <span className="flex">
                  <TitleEditor
                    title={s.title}
                    label="Thread title"
                    onSave={(title) => onRename(s.id, title)}
                    onDone={() => setRenaming(false)}
                    className="-mx-1 h-6 flex-1 md:h-5"
                  />
                </span>
                <DetailLine
                  s={s}
                  status={status}
                  badge={badge}
                  showProject={showProject}
                  projectName={projectName}
                  accentOf={accentOf}
                />
              </div>
            ) : (
              <>
                <button
                  type="button"
                  onClick={reordering ? undefined : () => onSelect(s.id)}
                  // A mouse or pen drags the row itself; a finger only ever
                  // drags by the handle, so a scroll is never a drag.
                  onPointerDown={drag?.onRowPointerDown}
                  aria-current={active ? "true" : undefined}
                  className={cn(
                    "focus-visible:ring-ring block w-full min-w-0 cursor-pointer rounded-lg px-2.5 py-2 text-left outline-none focus-visible:ring-2",
                    // The grip's column, in reorder mode only.
                    reordering && "cursor-default pl-11",
                  )}
                >
                  {/* Two matched lines: text on the left, a small mark on the
                      right — timestamp above, provider logo below. */}
                  <TitleLine
                    s={s}
                    isNew={badge === "new"}
                    reordering={reordering}
                  />
                  <DetailLine
                    s={s}
                    status={status}
                    badge={badge}
                    showProject={showProject}
                    projectName={projectName}
                    accentOf={accentOf}
                  />
                </button>

                {reordering ? (
                  <DragHandle s={s} drag={drag} />
                ) : (
                  <>
                    <RenameControl s={s} onRename={() => setRenaming(true)} />
                    <PinControl s={s} onSetPinned={onSetPinned} />

                    <DeleteControl s={s} onDelete={onDelete} />
                  </>
                )}
              </>
            )}
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent
          onCloseAutoFocus={(e) => {
            if (!renameChosen.current) return;
            renameChosen.current = false;
            e.preventDefault();
          }}
        >
          <ContextMenuItem
            onSelect={() => {
              renameChosen.current = true;
              setRenaming(true);
            }}
          >
            Rename
          </ContextMenuItem>
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
