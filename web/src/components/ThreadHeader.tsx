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

import { shallow, useThread, useThreadStore } from "~/app/threadStore";
import type { PanelControls } from "~/app/usePanel";
import type { ThreadHarness } from "~/app/useThreadHarness";
import type { TranscriptCopy } from "~/app/useTranscriptCopy";
import { liveJobCount } from "~/lib/jobs";
import type { CommandId } from "~/lib/keybindings";
import { threadTitle } from "~/lib/threadTitle";
import { cn } from "~/lib/utils";
import type { ThreadMeta, ThreadState } from "~/protocol";
import { useShortcutLabel } from "~/useShortcuts";

import { ExtensionStatuses } from "./ExtensionStatuses";
import { IconButton } from "./IconButton";
import { TitleEditor } from "./TitleEditor";
import { Button } from "./ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

// The permission-mode switcher is parked, not removed: changing modes mid-chat
// is not something we want to offer right now, and hiding it is cheaper to
// reverse than deleting it. Flip this to bring it back.
const SHOW_MODE_SWITCHER = false;

/** What the header's actions work on, on either layout. */
type Actions = {
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
  // The attached thread, as far as the bar shows it: a frame that changes
  // neither field leaves the header alone.
  const thread = useThread(useThreadStore(), headerFields, shallow);
  const actions = thread && { activeId, meta, onSetPinned, copy, panel, onShowMcp };
  return (
    <header className="px-2 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-2 md:px-3">
      <div className="flex items-center gap-2">
        {/* The open sidebar carries its own collapse button, so this one
            only appears when there is a closed sidebar to reopen. */}
        <IconButton
          label="Show threads"
          shortcut="toggleSidebar"
          onClick={onShowSidebar}
          className={cn(sidebarOpen && "hidden")}
        >
          <PanelLeftIcon className="size-3.5" />
        </IconButton>

        {actions ? (
          <>
            <HeaderTitle
              threadId={activeId}
              title={threadTitle(meta, thread.title)}
              editing={renamingId !== null && renamingId === activeId}
              onEdit={() => setRenamingId(activeId)}
              onDone={() => setRenamingId(null)}
              onRename={onRename}
            />
            {isDesktop && <ExtensionStatuses statuses={thread.extensionStatuses} inline />}
            {SHOW_MODE_SWITCHER && !thread.closed && <ModeSwitcher harness={harness} />}
            {isDesktop ? <DesktopActions actions={actions} /> : <PhoneActions actions={actions} />}
          </>
        ) : (
          <PendingTitle attaching={!!meta} creating={creating} />
        )}
      </div>
      {/* A phone's row is the title and one menu, with no room between: the
          chips take a line of their own, there only while there are any. */}
      {actions && !isDesktop && <ExtensionStatuses statuses={thread.extensionStatuses} inline={false} />}
    </header>
  );
}

/** The title row's stand-in while there is no thread state to show. */
function PendingTitle({ attaching, creating }: { attaching: boolean; creating: boolean }) {
  return (
    <span className={cn("flex-1 text-[13px]", creating && !attaching ? "font-medium" : "text-muted-foreground")}>
      {attaching ? "Attaching…" : creating ? "New thread" : ""}
    </span>
  );
}

/**
 * The open thread's name, and the pencil that turns it into a field. With a
 * mouse the pencil waits for hover; a touch screen has no hover to wait for,
 * phone or tablet, so there it is always showing.
 */
function HeaderTitle({
  threadId,
  title,
  editing,
  onEdit,
  onDone,
  onRename,
}: {
  threadId: string | null;
  title: string;
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
          // From md up it hugs the glyph, and a touch screen that wide (an
          // iPad) gets its 44px back as hit area alone.
          className="text-muted-foreground/70 hover:text-foreground hit-44 relative -ml-3 hover:bg-transparent md:ml-0 md:size-6 mouse:opacity-0 mouse:group-hover/title:opacity-100 mouse:focus-visible:opacity-100 dark:hover:bg-transparent"
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
  const { copy, panel } = actions;
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
        shortcut="copyTranscript"
        onClick={() => void copy.copyAll()}
      >
        {copy.copied ? <CheckIcon className="text-success" /> : <CopyIcon />}
      </IconButton>

      <IconButton
        label={panel.open ? "Hide the panel" : "Show the panel"}
        shortcut="togglePanel"
        onClick={panel.toggle}
        className={cn("relative", panel.open && "bg-accent")}
      >
        <PanelRightIcon />
        {/* A live agent-count badge: work is happening off-transcript. */}
        <JobsBadge className="-top-0.5 -right-0.5" />
      </IconButton>
    </>
  );
}

// A phone has no room for a row of buttons, so the same actions sit in one
// overflow menu.
function PhoneActions({ actions }: { actions: Actions }) {
  const { copy, panel, onShowMcp } = actions;
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
          <JobsBadge className="top-0.5 right-0.5" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-48">
        <DropdownMenuItem onSelect={panel.show}>
          <PanelRightIcon /> Open panel
          <ItemShortcut command="togglePanel" />
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onShowMcp}>
          <PlugIcon /> MCP servers
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={() => void copy.copyAll()}>
          {copy.copied ? <CheckIcon className="text-success" /> : <CopyIcon />}
          {copy.copied ? "Transcript copied" : "Copy transcript"}
          <ItemShortcut command="copyTranscript" />
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

// The menu only shows on a phone today, where hints are off and this renders
// nothing; it is here so the menu says the same as the tooltips wherever a
// keyboard and pointer do meet it.
function ItemShortcut({ command }: { command: CommandId }) {
  const label = useShortcutLabel(command);
  return label ? <DropdownMenuShortcut>{label}</DropdownMenuShortcut> : null;
}

// Selects the count itself, so a job's progress rerenders the badge only when
// it changes how many are live, and never the header around it.
function JobsBadge({ className }: { className: string }) {
  const live = useThread(useThreadStore(), liveJobsOf);
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

function headerFields(state: ThreadState | null) {
  return state && { title: state.title, closed: state.closed, extensionStatuses: state.extensionStatuses };
}

function liveJobsOf(state: ThreadState | null) {
  return state ? liveJobCount(state.jobs) : 0;
}
