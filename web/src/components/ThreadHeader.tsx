import {
  CheckIcon,
  CopyIcon,
  EllipsisIcon,
  PanelLeftIcon,
  PanelRightIcon,
  PencilIcon,
  PlugIcon,
  TagIcon,
} from "lucide-react";
import { useRef, useState } from "react";

import type { PanelControls } from "~/app/usePanel";
import type { ThreadHarness } from "~/app/useThreadHarness";
import type { TranscriptCopy } from "~/app/useTranscriptCopy";
import { liveJobCount } from "~/lib/jobs";
import { cn } from "~/lib/utils";
import type { Label, ThreadMeta, ThreadState } from "~/protocol";

import { IconButton } from "./IconButton";
import { LabelDot, LabelMenu, LabelMenuItems } from "./LabelMenu";
import { TitleEditor } from "./TitleEditor";
import { Button } from "./ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

// The permission-mode switcher is parked, not removed: changing modes mid-chat
// is not something we want to offer right now, and hiding it is cheaper to
// reverse than deleting it. Flip this to bring it back.
const SHOW_MODE_SWITCHER = false;

/** Filing the open thread, from the header on either layout. */
export type HeaderLabels = {
  labels: Label[];
  onSetLabel: (threadId: string, labelId: string) => void;
  onManage: () => void;
};

/** What the header's actions work on, on either layout. */
type Actions = {
  state: ThreadState;
  activeId: string | null;
  meta: ThreadMeta | undefined;
  labels: HeaderLabels;
  copy: TranscriptCopy;
  panel: PanelControls;
  /** The Skills page on its MCP tab, which asks this thread's session. */
  onShowMcp: () => void;
  /** Turns the title into a field. */
  onRename: () => void;
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
  labels,
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
  labels: HeaderLabels;
  harness: ThreadHarness;
  copy: TranscriptCopy;
  panel: PanelControls;
  onShowMcp: () => void;
}) {
  const [renaming, setRenaming] = useState(false);
  // The list entry first: it is where a rename lands, and the attached state
  // never hears about one. The state's title covers a new thread whose entry
  // has not caught up with its first prompt yet.
  const title = meta?.title || state?.title || "";
  const startRename = () => activeId && setRenaming(true);
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

      {state ? (
        <>
          {renaming && activeId ? (
            // Keyed to the thread: switching threads mid-edit drops the
            // field rather than saving one thread's draft over another's.
            <TitleEditor
              key={activeId}
              title={title}
              label="Thread title"
              onSave={(next) => onRename(activeId, next)}
              onDone={() => setRenaming(false)}
              className="h-8 flex-1 font-medium"
            />
          ) : (
            <div className="group/title flex min-w-0 flex-1 items-center gap-0.5">
              <p className="min-w-0 truncate text-[13px] font-medium">
                {title || "Untitled thread"}
              </p>
              {/* Desktop only, and only on hover: it sits right after the
                 text it edits. A phone reaches rename from the ⋯ menu,
                 which has the room. */}
              {isDesktop && activeId && (
                <IconButton
                  label="Rename thread"
                  onClick={startRename}
                  className="text-muted-foreground opacity-0 group-hover/title:opacity-100 focus-visible:opacity-100 [&_svg]:size-3.5"
                >
                  <PencilIcon />
                </IconButton>
              )}
            </div>
          )}
          {SHOW_MODE_SWITCHER && !state.closed && <ModeSwitcher harness={harness} />}
          {isDesktop ? (
            <DesktopActions
              actions={{ state, activeId, meta, labels, copy, panel, onShowMcp, onRename: startRename }}
            />
          ) : (
            <PhoneActions
              actions={{ state, activeId, meta, labels, copy, panel, onShowMcp, onRename: startRename }}
            />
          )}
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

// The label the open thread is filed under, and whether it can be filed at
// all: the menu is invisible until the user has defined a label.
function useThreadLabel({ activeId, meta, labels }: Actions) {
  const current = labels.labels.find((l) => l.id === meta?.labelId);
  const available = labels.labels.length > 0 && !!activeId;
  const select = (labelId: string) => activeId && labels.onSetLabel(activeId, labelId);
  return { current, available, select };
}

function DesktopActions({ actions }: { actions: Actions }) {
  const { state, meta, labels, copy, panel } = actions;
  const label = useThreadLabel(actions);
  return (
    <>
      {/* Filing the open thread: the same menu the sidebar row
          carries, so a thread can be labelled from either place. */}
      {label.available && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            {label.current ? (
              <Button
                variant="ghost"
                size="sm"
                aria-label={`Labelled ${label.current.name} — change label`}
                className="text-muted-foreground h-8 max-w-32 gap-1.5 px-2 text-[11px]"
              >
                <LabelDot color={label.current.color} />
                <span className="truncate">{label.current.name}</span>
              </Button>
            ) : (
              <Button variant="ghost" size="icon" aria-label="Label this thread" className="size-8">
                <TagIcon />
              </Button>
            )}
          </DropdownMenuTrigger>
          <LabelMenu
            labels={labels.labels}
            current={meta?.labelId}
            onSelect={label.select}
            onManage={labels.onManage}
          />
        </DropdownMenu>
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
  const { state, activeId, meta, labels, copy, panel, onShowMcp, onRename } = actions;
  const label = useThreadLabel(actions);
  // A closing menu hands focus back to its trigger, which would blur the
  // title field that Rename just opened. This says the close is ours.
  const renameChosen = useRef(false);
  return (
    // Not modal, for the same reason as the sidebar row's menu: a modal one
    // traps focus while it closes, which pulls it out of the rename field,
    // and the field has to take focus inside the tap for iOS to raise the
    // keyboard.
    <DropdownMenu modal={false}>
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
      <DropdownMenuContent
        align="end"
        className="min-w-48"
        onCloseAutoFocus={(e) => {
          if (!renameChosen.current) return;
          renameChosen.current = false;
          e.preventDefault();
        }}
      >
        {activeId && (
          <>
            <DropdownMenuItem
              onSelect={() => {
                renameChosen.current = true;
                onRename();
              }}
            >
              <PencilIcon /> Rename thread
            </DropdownMenuItem>
            <DropdownMenuSeparator />
          </>
        )}
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
        {label.available && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuSub>
              <DropdownMenuSubTrigger>
                {label.current ? (
                  <>
                    <LabelDot color={label.current.color} />
                    <span className="truncate">{label.current.name}</span>
                  </>
                ) : (
                  <>
                    <TagIcon /> Label thread
                  </>
                )}
              </DropdownMenuSubTrigger>
              <DropdownMenuSubContent className="min-w-40">
                <LabelMenuItems
                  labels={labels.labels}
                  current={meta?.labelId}
                  onSelect={label.select}
                  onManage={labels.onManage}
                />
              </DropdownMenuSubContent>
            </DropdownMenuSub>
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
