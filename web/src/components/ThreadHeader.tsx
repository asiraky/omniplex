import {
  ArchiveIcon,
  BotIcon,
  CheckIcon,
  ChevronRightIcon,
  CopyIcon,
  CornerLeftUpIcon,
  EllipsisIcon,
  HatGlassesIcon,
  HistoryIcon,
  LinkIcon,
  PanelLeftIcon,
  PanelRightIcon,
  PencilIcon,
  PinIcon,
  PinOffIcon,
  PlugIcon,
  RotateCwIcon,
  SearchIcon,
  SettingsIcon,
  SquarePenIcon,
  TerminalIcon,
  XIcon,
} from "lucide-react";
import { useRef, useState } from "react";

import { shallow, useThread, useThreadStore } from "~/app/threadStore";
import type { PanelControls } from "~/app/usePanel";
import type { ThreadHarness } from "~/app/useThreadHarness";
import type { TranscriptCopy } from "~/app/useTranscriptCopy";
import { useAgentNav } from "~/lib/agentNav";
import { liveJobCount } from "~/lib/jobs";
import type { CommandId } from "~/lib/keybindings";
import { threadTitle } from "~/lib/threadTitle";
import { cn } from "~/lib/utils";
import type { Agent, ThreadMeta, ThreadState } from "~/protocol";
import { useShortcutLabel } from "~/useShortcuts";

import { RestartAgentDialog } from "./agents/AgentDialogs";
import { NewAgentThreadDialog } from "./agents/NewAgentThreadDialog";
import { ExtensionStatuses } from "./ExtensionStatuses";
import { IconButton } from "./IconButton";
import { TitleEditor } from "./TitleEditor";
import { Button } from "./ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Spinner } from "./ui/spinner";

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
  link: ThreadLinkCopy;
  panel: PanelControls;
  /** The Skills page on its MCP tab, which asks this thread's session. */
  onShowMcp: () => void;
  /** Opens the transcript's find bar, which takes focus. */
  onFind: () => void;
  harness: ThreadHarness;
  /** The agent this thread runs for: its page and New thread replace pin. */
  agent: Agent | undefined;
  onNewAgentThread: () => void;
  /** Asks before restarting an agent's thread, which kills its shells. */
  onRestartAgent: () => void;
};

/** Copying a thread's /thread/<id> link, and whether one was just copied. */
export type ThreadLinkCopy = { copied: boolean; copy: (threadId: string) => void };

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
  link,
  panel,
  onShowMcp,
  onFind,
  onClose,
  parent,
  onOpenThread,
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
  link: ThreadLinkCopy;
  panel: PanelControls;
  onShowMcp: () => void;
  onFind: () => void;
  /** Asks to close an assistant chat, which deletes it. */
  onClose?: (meta: ThreadMeta) => void;
  /** The listed thread that delegated this one: the title offers the way back. */
  parent?: ThreadMeta;
  onOpenThread?: (id: string) => void;
}) {
  // The thread the title field is open on: switching threads closes it rather
  // than opening the next thread's name for editing.
  const [renamingId, setRenamingId] = useState<string | null>(null);
  // The attached thread, as far as the bar shows it: a frame that changes
  // neither field leaves the header alone.
  const thread = useThread(useThreadStore(), headerFields, shallow);
  // An agent's thread belongs to the agent: its name, revisions and New
  // thread stand where a thread's own title, pin and rename would.
  const { current: agent, openAgent } = useAgentNav();
  const [newThreadOpen, setNewThreadOpen] = useState(false);
  const onNewAgentThread = () => setNewThreadOpen(true);
  const [restartOpen, setRestartOpen] = useState(false);
  const onRestartAgent = () => setRestartOpen(true);
  const actions = thread && {
    activeId,
    meta,
    onSetPinned,
    copy,
    link,
    panel,
    onShowMcp,
    onFind,
    harness,
    agent,
    onNewAgentThread,
    onRestartAgent,
  };
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
            {agent ? (
              <AgentTitle agent={agent} onOpen={() => openAgent(agent.id, "prompt")} />
            ) : (
              <HeaderTitle
                threadId={activeId}
                title={threadTitle(meta, thread.title)}
                editing={renamingId !== null && renamingId === activeId}
                onEdit={() => setRenamingId(activeId)}
                onDone={() => setRenamingId(null)}
                onRename={onRename}
                delegatedBy={parent && onOpenThread ? { parent, open: () => onOpenThread(parent.id) } : undefined}
              />
            )}
            {meta?.private && (
              <span
                title="Kept in memory only. Closing it deletes it."
                className="text-muted-foreground flex shrink-0 items-center gap-1 rounded-full border px-2 py-0.5 text-[11px]"
              >
                <HatGlassesIcon aria-hidden className="size-3" />
                Private
              </span>
            )}
            {isDesktop && <ExtensionStatuses statuses={thread.extensionStatuses} inline />}
            {SHOW_MODE_SWITCHER && !thread.closed && <ModeSwitcher harness={harness} />}
            {isDesktop ? <DesktopActions actions={actions} /> : <PhoneActions actions={actions} />}
            {/* An assistant chat has nothing to land, so being done with it
                is closing it, one tap away. */}
            {meta && !meta.projectId && onClose && (
              <IconButton
                label={meta.private ? "Close private chat" : "Close chat"}
                onClick={() => onClose(meta)}
                className="text-muted-foreground hover:text-foreground"
              >
                <XIcon />
              </IconButton>
            )}
          </>
        ) : (
          <PendingTitle attaching={!!meta} creating={creating} />
        )}
      </div>
      {/* A phone's row is the title and one menu, with no room between: the
          chips take a line of their own, there only while there are any. */}
      {actions && !isDesktop && <ExtensionStatuses statuses={thread.extensionStatuses} inline={false} />}
      {agent && newThreadOpen && (
        <NewAgentThreadDialog agent={agent} command={harness.command} onClose={() => setNewThreadOpen(false)} />
      )}
      {agent && restartOpen && (
        <RestartAgentDialog agent={agent} command={harness.command} onClose={() => setRestartOpen(false)} />
      )}
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
  delegatedBy,
}: {
  threadId: string | null;
  title: string;
  editing: boolean;
  onEdit: () => void;
  onDone: () => void;
  onRename: (threadId: string, title: string) => void;
  /** The thread that delegated this one, and opening it. */
  delegatedBy?: { parent: ThreadMeta; open: () => void };
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
  const row = (
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
  if (!delegatedBy) return row;
  const parentTitle = delegatedBy.parent.title || "Untitled";
  // A delegated thread has no sidebar row of its own, so the way back to the
  // thread that started it sits under the title, a line of its own.
  return (
    <div className="flex min-w-0 flex-1 flex-col">
      {row}
      <button
        type="button"
        onClick={delegatedBy.open}
        className="text-muted-foreground hover:text-foreground focus-visible:ring-ring -mt-0.5 flex min-h-7 max-w-full items-center gap-1 self-start rounded-sm text-left text-[12px] outline-none focus-visible:ring-2"
      >
        <CornerLeftUpIcon aria-hidden className="size-3 shrink-0" />
        <span className="min-w-0 truncate">
          Delegated by <span className="font-medium">{parentTitle}</span>
        </span>
      </button>
    </div>
  );
}

// A prompt revision saved but not yet running: the restart onto it waits for
// the turn to end. The agent says when a restart is pending; the revisions
// alone could disagree for good if that restart never comes.
function restartWaiting(agent: Agent) {
  return !!agent.restartPending && agent.livePromptRev !== undefined && agent.livePromptRev < agent.promptRev;
}

/**
 * An agent thread's title: the agent's name and the prompt revision it runs
 * on, one tap to the agent's page. No pencil: the name belongs to the agent
 * and changes in its Settings. While a saved revision waits on the turn, the
 * pill says which one is coming.
 */
function AgentTitle({ agent, onOpen }: { agent: Agent; onOpen: () => void }) {
  const waiting = restartWaiting(agent);
  return (
    <div className="flex min-w-0 flex-1 items-center">
      <button
        type="button"
        onClick={onOpen}
        title={`Open ${agent.name}`}
        className="hover:bg-accent flex min-h-11 min-w-0 items-center gap-1.5 rounded-md px-1.5 md:min-h-8"
      >
        <BotIcon aria-hidden className="text-primary size-4 shrink-0" />
        <span className="min-w-0 truncate text-[13px] font-medium">{agent.name}</span>
        <span
          className={cn(
            "flex shrink-0 items-center gap-1 rounded-full border px-1.5 font-mono text-[10.5px] leading-4",
            waiting ? "border-attention/50 bg-attention/15 text-attention-foreground" : "text-muted-foreground",
          )}
        >
          {waiting && <RotateCwIcon aria-hidden className="size-2.5" />}
          {waiting ? (
            <>
              r{agent.livePromptRev}
              <span aria-hidden>→</span>r{agent.promptRev}
            </>
          ) : (
            `r${agent.promptRev}`
          )}
        </span>
        <ChevronRightIcon aria-hidden className="text-muted-foreground size-3.5 shrink-0" />
      </button>
    </div>
  );
}

/**
 * The agent's part of a thread menu: its page's tabs, then the two ways to
 * move its thread on. Restart keeps the transcript; New thread archives it,
 * so it asks first.
 */
function AgentMenuItems({ actions }: { actions: Actions }) {
  const { openAgent } = useAgentNav();
  const agent = actions.agent;
  if (!agent) return null;
  const restart = threadRestart(actions);
  return (
    <>
      <DropdownMenuLabel className="text-muted-foreground text-[11px] font-normal">{agent.name}</DropdownMenuLabel>
      <DropdownMenuItem onSelect={() => openAgent(agent.id, "prompt")}>
        <TerminalIcon />
        <MenuText title="Prompt" sub={`r${agent.promptRev}`} />
      </DropdownMenuItem>
      <DropdownMenuItem onSelect={() => openAgent(agent.id, "compact")}>
        <ArchiveIcon />
        <MenuText title="Compaction" sub={`c${agent.compactRev}`} />
      </DropdownMenuItem>
      <DropdownMenuItem onSelect={() => openAgent(agent.id, "log")}>
        <HistoryIcon />
        <MenuText title="Log" sub={`${agent.epoch} compaction${agent.epoch === 1 ? "" : "s"}`} />
      </DropdownMenuItem>
      <DropdownMenuItem onSelect={() => openAgent(agent.id, "settings")}>
        <SettingsIcon /> Settings
      </DropdownMenuItem>
      <DropdownMenuSeparator />
      {restart && (
        <DropdownMenuItem onSelect={restart.run} disabled={restart.restarting}>
          {restart.restarting ? <Spinner /> : <RotateCwIcon />}
          <MenuText
            title={restart.restarting ? "Restarting thread…" : restart.label}
            sub={restart.pending ? "Waiting for the turn to end" : "Keeps the transcript"}
          />
        </DropdownMenuItem>
      )}
      <DropdownMenuItem onSelect={actions.onNewAgentThread}>
        <SquarePenIcon />
        <MenuText title="New thread…" sub="Archives this one" />
      </DropdownMenuItem>
    </>
  );
}

function MenuText({ title, sub }: { title: string; sub: string }) {
  return (
    <span className="flex min-w-0 flex-col">
      <span>{title}</span>
      <span className="text-muted-foreground text-[11px]">{sub}</span>
    </span>
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
function threadPin({ activeId, meta, onSetPinned, agent }: Actions) {
  const pinned = !!meta?.pinned;
  // An agent's thread is reached through the agent, never pinned.
  const toggle = activeId && !agent ? () => onSetPinned(activeId, !pinned) : null;
  return { pinned, toggle };
}

// Restarting the harness on the setup as it is now, or taking back one that
// waits on the running turn: the same toggle in either layout. An agent's
// thread asks first.
function threadRestart({ harness, agent, onRestartAgent }: Actions) {
  if (!harness.canRestart) return null;
  const { restartPending: pending, restarting } = harness;
  return {
    pending,
    restarting,
    label: pending ? "Cancel restart" : restarting ? "Restarting thread" : "Restart thread",
    run: pending ? harness.cancelRestart : agent ? onRestartAgent : harness.restartThread,
  };
}

function DesktopActions({ actions }: { actions: Actions }) {
  const { activeId, copy, link, panel } = actions;
  const pin = threadPin(actions);
  const restart = threadRestart(actions);
  return (
    <>
      {/* An agent's thread restarts after every prompt change, so there it is
          a word, not an icon to decode. */}
      {restart && actions.agent && (
        <Button
          variant="ghost"
          size="sm"
          onClick={restart.run}
          disabled={restart.restarting}
          className={cn("h-8 shrink-0 gap-1.5 px-2 text-[12.5px]", restart.pending && "bg-accent")}
        >
          {restart.restarting ? <Spinner /> : <RotateCwIcon />}
          {restart.pending ? "Cancel restart" : restart.restarting ? "Restarting…" : "Restart"}
        </Button>
      )}

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

      {activeId && (
        <IconButton
          label={link.copied ? "Thread link copied" : "Copy thread link"}
          onClick={() => link.copy(activeId)}
        >
          {link.copied ? <CheckIcon className="text-success" /> : <LinkIcon />}
        </IconButton>
      )}

      {restart && !actions.agent && (
        <IconButton
          label={restart.label}
          onClick={restart.run}
          disabled={restart.restarting}
          className={cn(restart.pending && "bg-accent")}
        >
          {restart.restarting ? <Spinner /> : <RotateCwIcon />}
        </IconButton>
      )}

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

      {actions.agent && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label="Agent actions" className="size-8 shrink-0">
              <EllipsisIcon />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-52">
            <AgentMenuItems actions={actions} />
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </>
  );
}

// A phone has no room for a row of buttons, so the same actions sit in one
// overflow menu.
function PhoneActions({ actions }: { actions: Actions }) {
  const { activeId, copy, link, panel, onShowMcp, onFind } = actions;
  const pin = threadPin(actions);
  const restart = threadRestart(actions);
  // Set by "Find in thread", read as the menu closes in its wake.
  const finding = useRef(false);
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
      <DropdownMenuContent
        align="end"
        className="min-w-48"
        onCloseAutoFocus={(e) => {
          // The menu traps focus while open, so the field the bar focused on
          // opening lost it straight back; and closing would hand it to the
          // trigger. Ask again now the menu has let go: the bar refocuses its
          // field on every ask.
          if (!finding.current) return;
          finding.current = false;
          e.preventDefault();
          onFind();
        }}
      >
        {actions.agent && (
          <>
            <AgentMenuItems actions={actions} />
            <DropdownMenuSeparator />
          </>
        )}
        <DropdownMenuItem
          onSelect={() => {
            finding.current = true;
            onFind();
          }}
        >
          <SearchIcon /> Find in thread
          <ItemShortcut command="findInThread" />
        </DropdownMenuItem>
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
        {activeId && (
          <DropdownMenuItem onSelect={() => link.copy(activeId)}>
            {link.copied ? <CheckIcon className="text-success" /> : <LinkIcon />}
            {link.copied ? "Thread link copied" : "Copy thread link"}
          </DropdownMenuItem>
        )}
        {/* An agent's restart sits with its other items above. */}
        {(pin.toggle || (restart && !actions.agent)) && <DropdownMenuSeparator />}
        {pin.toggle && (
          <DropdownMenuItem onSelect={pin.toggle}>
            {pin.pinned ? <PinOffIcon /> : <PinIcon />}
            {pin.pinned ? "Unpin thread" : "Pin thread"}
          </DropdownMenuItem>
        )}
        {restart && !actions.agent && (
          <DropdownMenuItem onSelect={restart.run} disabled={restart.restarting}>
            {restart.restarting ? <Spinner /> : <RotateCwIcon />}
            {restart.restarting ? "Restarting thread…" : restart.label}
          </DropdownMenuItem>
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
