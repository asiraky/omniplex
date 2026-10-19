import {
  CheckIcon,
  CopyIcon,
  EllipsisIcon,
  PanelLeftIcon,
  PanelRightIcon,
  PencilIcon,
  PinIcon,
  PinOffIcon,
  PlugIcon,
} from "lucide-react";
import { useState } from "react";

import type { PanelControls } from "~/app/usePanel";
import type { ThreadHarness } from "~/app/useThreadHarness";
import type { TranscriptCopy } from "~/app/useTranscriptCopy";
import { liveJobCount } from "~/lib/jobs";
import { threadTitle } from "~/lib/threadTitle";
import { cn } from "~/lib/utils";
import type { ThreadMeta, ThreadState } from "~/protocol";

import { IconButton } from "./IconButton";
import { TitleEditor } from "./TitleEditor";
import { Button } from "./ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

// The permission-mode switcher is parked, not removed: changing modes mid-chat
// is not something we want to offer right now, and hiding it is cheaper to
// reverse than deleting it. Flip this to bring it back.
const SHOW_MODE_SWITCHER = false;

/** What the header's actions work on, on either layout. */
type Actions = {
  state: ThreadState;
  activeId: string | null;
  meta: ThreadMeta | undefined;
  onSetPinned: (threadId: string, pinned: boolean) => void;
  copy: TranscriptCopy;
  panel: PanelControls;
  /** The Skills page on its MCP tab, which asks this thread's session. */
  onShowMcp: () => void;
};

/** The bar above the content column: the thread's title and its actions. */
export function ThreadHeader({
  sidebarOpen,
  onShowSidebar,
  state,
  activeId,
  meta,
  onRename,
  creating,
  isDesktop,
  onSetPinned,
  harness,
  copy,
  panel,
  onShowMcp,
}: {
  sidebarOpen: boolean;
  onShowSidebar: () => void;
  state: ThreadState | null;
  activeId: string | null;
  meta: ThreadMeta | undefined;
  onRename: (threadId: string, title: string) => void;
  creating: boolean;
  isDesktop: boolean;
  onSetPinned: (threadId: string, pinned: boolean) => void;
  harness: ThreadHarness;
  copy: TranscriptCopy;
  panel: PanelControls;
  onShowMcp: () => void;
}) {
  // The thread the title field is open on: switching threads closes it rather
  // than opening the next thread's name for editing.
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const actions = state && { state, activeId, meta, onSetPinned, copy, panel, onShowMcp };
  return (
    <header className="flex items-center gap-2 px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:px-3">
      {/* The open sidebar carries its own collapse button, so this one
          only appears when there is a closed sidebar to reopen. */}
      <IconButton
        label="Show threads"
        onClick={onShowSidebar}
        className={cn(sidebarOpen && "hidden")}
      >
        <PanelLeftIcon className="size-3.5" />
      </IconButton>

      {actions ? (
        <>
          <HeaderTitle
            threadId={activeId}
            title={threadTitle(meta, actions.state)}
            isDesktop={isDesktop}
            editing={renamingId !== null && renamingId === activeId}
            onEdit={() => setRenamingId(activeId)}
            onDone={() => setRenamingId(null)}
            onRename={onRename}
          />
          {SHOW_MODE_SWITCHER && !actions.state.closed && <ModeSwitcher harness={harness} />}
          {isDesktop ? <DesktopActions actions={actions} /> : <PhoneActions actions={actions} />}
        </>
      ) : (
        <span
          className={cn(
            "flex-1 text-[13px]",
            creating && !meta ? "font-medium" : "text-muted-foreground",
          )}
        >
          {meta ? "Attaching…" : creating ? "New thread" : ""}
        </span>
      )}
    </header>
  );
}

/**
 * The open thread's name, and the pencil that turns it into a field. On a
 * desktop the pencil waits for hover; a phone has no hover, so there it is
 * always showing.
 */
function HeaderTitle({
  threadId,
  title,
  isDesktop,
  editing,
  onEdit,
  onDone,
  onRename,
}: {
  threadId: string | null;
  title: string;
  isDesktop: boolean;
  editing: boolean;
  onEdit: () => void;
  onDone: () => void;
  onRename: (threadId: string, title: string) => void;
}) {
  if (editing && threadId) {
    return (
      <TitleEditor
        title={title}
        label="Thread title"
        onSave={(next) => onRename(threadId, next)}
        onDone={onDone}
        className="h-8 flex-1 font-medium"
      />
    );
  }
  return (
    <div className="group/title flex min-w-0 flex-1 items-center gap-0.5">
      <p className="min-w-0 truncate text-[13px] font-medium">{title || "Untitled thread"}</p>
      {threadId && (
        <IconButton
          label="Rename thread"
          onClick={onEdit}
          // A 12px glyph, smaller than the 13px title, and faint until
          // pointed at. On a phone the button keeps its 44px target but
          // tucks in so the glyph sits just after the text, not 16px off.
          className={cn(
            "text-muted-foreground/70 hover:text-foreground -ml-3 hover:bg-transparent md:ml-0 md:size-6 dark:hover:bg-transparent",
            isDesktop && "opacity-0 group-hover/title:opacity-100 focus-visible:opacity-100",
          )}
        >
          <PencilIcon className="size-3" strokeWidth={1.75} />
        </IconButton>
      )}
    </div>
  );
}

function ModeSwitcher({ harness }: { harness: ThreadHarness }) {
  if (harness.modeOptions.length === 0) return null;
  return (
    <Select value={harness.currentModeId} onValueChange={harness.switchMode}>
      {/* Every mode gets the same chip: one that changed shape or
          colour by mode would jitter the header and shout at the
          user about a choice they already made deliberately. */}
      <SelectTrigger
        aria-label="Permission mode"
        className="h-8 w-auto shrink-0 gap-1 px-2 text-[11px]"
      >
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {harness.modeOptions.map((m) => (
          <SelectItem key={m.id} value={m.id}>
            {m.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// Whether the open thread is pinned, and the toggle: the same one the sidebar
// row carries, so a thread can be pinned from either place.
function threadPin({ activeId, meta, onSetPinned }: Actions) {
  const pinned = !!meta?.pinned;
  const toggle = activeId ? () => onSetPinned(activeId, !pinned) : null;
  return { pinned, toggle };
}

function DesktopActions({ actions }: { actions: Actions }) {
  const { state, copy, panel } = actions;
  const pin = threadPin(actions);
  return (
    <>
      {pin.toggle && (
        <IconButton
          label={pin.pinned ? "Unpin thread" : "Pin thread"}
          onClick={pin.toggle}
          className={pin.pinned ? "text-foreground" : "text-muted-foreground hover:text-foreground"}
        >
          <PinIcon className={cn(pin.pinned && "fill-current")} />
        </IconButton>
      )}

      <IconButton
        label={copy.copied ? "Transcript copied" : "Copy transcript"}
        onClick={() => void copy.copyAll()}
      >
        {copy.copied ? <CheckIcon className="text-success" /> : <CopyIcon />}
      </IconButton>

      <IconButton
        label={panel.open ? "Hide the panel" : "Show the panel"}
        onClick={panel.toggle}
        className={cn("relative", panel.open && "bg-accent")}
      >
        <PanelRightIcon />
        {/* A live agent-count badge: work is happening off-transcript. */}
        <JobsBadge state={state} className="-top-0.5 -right-0.5" />
      </IconButton>
    </>
  );
}

// A phone has no room for a row of buttons, so the same actions sit in one
// overflow menu.
function PhoneActions({ actions }: { actions: Actions }) {
  const { state, copy, panel, onShowMcp } = actions;
  const pin = threadPin(actions);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="More thread actions"
          className="relative size-11 shrink-0"
        >
          <EllipsisIcon />
          <JobsBadge state={state} className="top-0.5 right-0.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        <DropdownMenuItem onSelect={panel.show}>
          <PanelRightIcon /> Open panel
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onShowMcp}>
          <PlugIcon /> MCP servers
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void copy.copyAll()}>
          {copy.copied ? <CheckIcon className="text-success" /> : <CopyIcon />}
          {copy.copied ? "Transcript copied" : "Copy transcript"}
        </DropdownMenuItem>
        {pin.toggle && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem onSelect={pin.toggle}>
              {pin.pinned ? <PinOffIcon /> : <PinIcon />}
              {pin.pinned ? "Unpin thread" : "Pin thread"}
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function JobsBadge({ state, className }: { state: ThreadState; className: string }) {
  const live = liveJobCount(state.jobs);
  if (live === 0) return null;
  return (
    <span
      className={cn(
        "bg-primary text-primary-foreground absolute flex size-3.5 items-center justify-center rounded-full text-[9px] tabular-nums",
        className,
      )}
    >
      {live}
    </span>
  );
}
