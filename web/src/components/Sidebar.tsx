import {
  ActivityIcon,
  BlocksIcon,
  FolderPlusIcon,
  SearchIcon,
  SettingsIcon,
  PanelLeftIcon,
  SquarePenIcon,
} from "lucide-react";
import {
  memo,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";

import { KEYBINDINGS_PASS } from "~/app/useKeybindings";
import type { ProjectView } from "~/app/useProjectView";
import type { ConnectionStatus } from "~/client";
import {
  DeleteThreadDialog,
  useDeleteThread,
} from "~/components/DeleteThreadDialog";
import { IconButton } from "~/components/IconButton";
import { ThreadFilter } from "~/components/ThreadFilter";
import { StatusDot } from "~/components/StatusDot";
import { ThemeToggle } from "~/components/ThemeToggle";
import { Button } from "~/components/ui/button";
import { KbdChord } from "~/components/ui/kbd";
import { Separator } from "~/components/ui/separator";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { jumpCommand, type CommandId } from "~/lib/keybindings";
import { isTitleEditor } from "~/lib/threadTitle";
import { cn } from "~/lib/utils";
import { visibleByProject } from "~/projectGroups";
import { newCount } from "~/threadStatus";
import type { Project, ThreadMeta } from "~/protocol";
import { sectionRows, sidebarSections, type SidebarSection } from "~/sidebarOrder";
import { useIsDesktop } from "~/useMediaQuery";
import { useShortcut, useShowShortcuts } from "~/useShortcuts";
import { Wordmark } from "./Logo";
import { ProjectGroup } from "./sidebar/ProjectGroup";
import { SortableRows, type RowDrag } from "./sidebar/SortableRows";
import { ThreadRow } from "./sidebar/ThreadRow";

// How long a row takes to fold away once it has left the list. Kept in step
// with the duration on the row itself.
const EXIT_MS = 260;

const WIDTH_KEY = "omniplex.sidebarWidth";
const MIN_WIDTH = 208;
const MAX_WIDTH = 480;
const DEFAULT_WIDTH = 288;

interface SidebarProps {
  threads: ThreadMeta[];
  activeId: string | null;
  status: ConnectionStatus;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (id: string) => void;
  /** New thread, pressed: opens the "New thread in…" step. */
  onNewThreadIn: () => void;
  /** New thread, shift-clicked: a draft in the active thread's project, no questions. */
  onNew: () => void;
  /** Opens the command palette, which doubles as thread search. */
  onOpenPalette: () => void;
  /**
   * The filter, the folds and the grouping, held by App so the thread
   * shortcuts walk the order this list shows.
   */
  projectView: ProjectView;
  /** ⌘ is held: the first nine rows show the digit that opens them. */
  showJumpHints: boolean;
  /**
   * removeWorktree is the user's answer to the dialog's checkbox, never
   * inferred. The promise, if one is returned, only says the request was
   * accepted — the delete is finished when the thread leaves `threads`.
   */
  onDelete: (id: string, removeWorktree: boolean) => void | Promise<unknown>;
  /** Opens the "how to reach this server" panel. */
  onShowAccess: () => void;
  /** Opens the account-level Usage page: cost history, tokens, limits. */
  onShowUsage: () => void;
  /** Opens the Skills page: what the harnesses can run, with or without a thread. */
  onShowSkills: () => void;
  /** Opens settings: your defaults, providers and their sign-ins, each project. */
  onShowSettings: () => void;
  // Supplied by the server via the adapter; the sidebar knows no harness names.
  accentOf: (harness: string) => string | undefined;
  /**
   * The project registry, in the server's order. The sidebar groups by it and
   * filters by it, so unlike projectName it needs the list itself.
   */
  projects: Project[];
  projectName: (id?: string) => string | undefined;
  /** The project's own checkout, which is never a worktree omniplex may remove. */
  projectFolders: (id?: string) => string[];
  /**
   * Pins a thread to the top of the list, or unpins it. `threads` must show
   * the change at once, the way it does a reorder.
   */
  onSetPinned: (threadId: string, pinned: boolean) => void;
  /** Opens the new project dialog, which App owns. */
  onNewProject: () => void;
  /** Flips the unread flag by hand — the row's "come back to this" action. */
  onSetUnread: (threadId: string, unread: boolean) => void;
  /** Gives a thread the title the user typed. */
  onRename: (threadId: string, title: string) => void;
  /** Copies the thread's /thread/<id> link. */
  onCopyLink: (threadId: string) => void;
  /**
   * Whether Back on a phone closes the drawer. With nothing selected the
   * drawer is the landing screen and there is nothing behind it, so Back has
   * to leave it alone and do whatever Back did before.
   */
  backCloses: boolean;
  /**
   * Moves a thread to a new place in the user's order. `threads` must already
   * be in that order, and must show the move at once: the row the user just
   * dropped stays where it landed rather than waiting for the server.
   */
  onReorder: (threadId: string, position: number) => void;
}

/**
 * Everything the delete takes: the confirmation, the wait, and the row's exit.
 *
 * It lives above the sidebar's two shapes rather than inside the list, because
 * the list does not outlive either of them. The mobile sheet closes when a
 * thread is selected — which deleting a row does — and crossing the `md`
 * breakpoint swaps the sheet for the docked panel outright. Both unmount the
 * list, and a delete held in there would lose its dialog, its ordering and its
 * animation mid-flight.
 */
function useDeleteFlow({
  threads,
  onDelete,
  projectFolders,
}: Pick<SidebarProps, "threads" | "onDelete" | "projectFolders">) {
  // Two pieces of state, and both are about the *list* — the confirmation, the
  // guards and the wait all live in useDeleteThread, which the transcript's
  // "this landed" prompt opens too.
  //
  // `frozen` pins the list to the order it had when Delete was pressed. The
  //   sort is a stable created-at anchor now, so activity can no longer move
  //   the row — but the list can still change shape mid-delete (a thread
  //   created from a paired device), and the departing row's neighbours must
  //   hold still under the animation.
  // `exiting` keeps the row on screen, in its own place, for one last
  //   animation after it has already left the list.
  const [frozen, setFrozen] = useState<string[] | null>(null);
  const [exiting, setExiting] = useState<ThreadMeta | null>(null);

  const thread = useDeleteThread({
    threads,
    onDelete,
    projectFolders,
    onStart: () => {
      setFrozen(threads.map((s) => s.id));
      setExiting(null);
    },
    // The request never went, so there is no departure to animate.
    onRefused: () => setFrozen(null),
    // The row has left the list. The hook has already stopped waiting; all
    // that is left here is to keep the row on screen long enough to leave.
    onDeparted: (target) => setExiting(target),
    // Teardown failed, so the row is staying. App is already asking what to do
    // about it, and the list has no departure to hold its order for.
    onFailed: () => setFrozen(null),
  });
  const { deleting } = thread;

  // The animation is the only thing still holding either of these.
  useEffect(() => {
    if (!exiting) return;
    const t = setTimeout(() => {
      setExiting(null);
      setFrozen(null);
    }, EXIT_MS + 60);
    return () => clearTimeout(t);
  }, [exiting]);

  // While a delete is in flight the sidebar renders the order it had when the
  // user committed to it, with the departing row put back at its own index.
  const rows = useMemo(() => {
    if (!frozen) return threads;
    const rank = new Map(frozen.map((id, i) => [id, i]));
    // Anything the server has added since sorts ahead, which is where a new
    // thread lands in the user's order anyway.
    const list = [...threads].sort((a, b) => (rank.get(a.id) ?? -1) - (rank.get(b.id) ?? -1));
    if (exiting && !threads.some((s) => s.id === exiting.id)) {
      const at = frozen.indexOf(exiting.id);
      if (at >= 0) list.splice(Math.min(at, list.length), 0, exiting);
    }
    return list;
  }, [threads, frozen, exiting]);

  return { thread, rows, ask: thread.ask, deleting, exiting };
}

type DeleteFlow = ReturnType<typeof useDeleteFlow>;

function ThreadList({
  flow,
  activeId,
  onSelect,
  accentOf,
  projects,
  projectName,
  projectView,
  onSetPinned,
  onSetUnread,
  onRename,
  onCopyLink,
  onReorder,
  reordering,
  showJumpHints,
}: Pick<
  SidebarProps,
  | "showJumpHints"
  | "activeId"
  | "onSelect"
  | "accentOf"
  | "projects"
  | "projectName"
  | "onSetPinned"
  | "onSetUnread"
  | "onRename"
  | "onCopyLink"
  | "onReorder"
> & {
  flow: DeleteFlow;
  /** Reorder mode: rows show a grip and open nothing when tapped. */
  reordering: boolean;
  projectView: ProjectView;
}) {
  const { rows, ask, deleting, exiting } = flow;

  // The sections are cut from the delete flow's rows — frozen order, exiting
  // row and all — so a departing thread folds away in place instead of
  // vanishing the instant the filter is recomputed. Nothing in the cut
  // reorders, so the row still leaves from exactly where it stood. It is the
  // same cut the thread shortcuts walk.
  const sections = sidebarSections(rows, projects, projectView);
  // The digit each of the first nine open rows answers to, while ⌘ is held.
  const jumps = new Map<string, CommandId>();
  if (showJumpHints) {
    sectionRows(sections, projectView.collapsed)
      .slice(0, 9)
      .forEach((s, i) => jumps.set(s.id, jumpCommand(i)!));
  }
  // A delete holds the list still for its exit animation, frozen order and
  // all; a drop computed against that order would land somewhere else.
  const sortable = !deleting && !exiting;

  // No threads is no threads: the filter is a way to narrow a list, not a
  // thing to show in place of one.
  if (rows.length === 0) {
    return (
      <p className="text-muted-foreground px-3 py-10 text-center text-[13px]">
        No threads yet.
        <br />
        <span className="text-[12px]">Start one to see it here.</span>
      </p>
    );
  }

  // There are threads; the filter is the only reason none of them are here,
  // so the way out of that is the message rather than something to go hunting
  // for in the header.
  if (sections.length === 0) {
    return (
      <div className="px-3 py-10 text-center">
        <p className="text-muted-foreground text-[13px]">
          {rows.length} thread{rows.length === 1 ? "" : "s"} hidden by the filter.
        </p>
        <Button
          variant="outline"
          size="sm"
          onClick={projectView.onShowAll}
          className="mt-3 h-8"
        >
          Show all
        </Button>
      </div>
    );
  }

  const row = (s: ThreadMeta, showProject: boolean, drag: RowDrag | undefined) => (
    <ThreadRow
      key={s.id}
      s={s}
      showProject={showProject}
      active={s.id === activeId}
      leaving={exiting?.id === s.id}
      going={deleting?.id === s.id}
      reordering={reordering}
      drag={drag}
      accentOf={accentOf}
      projectName={projectName}
      onSelect={onSelect}
      onSetPinned={onSetPinned}
      onSetUnread={onSetUnread}
      onRename={onRename}
      onCopyLink={onCopyLink}
      onDelete={ask}
      jumpHint={jumps.get(s.id)}
    />
  );

  // A drag reorders among the rows it can see, so each run of rows is its own
  // sortable list: Pinned, then the rest whole when flat, or one group's rows
  // when grouped. Each run is a subsequence of the user's order, so a midpoint
  // of two neighbours in it lands between them in the whole list too.
  const run = (threads: ThreadMeta[], showProject: boolean) => (
    <SortableRows rows={threads} enabled={sortable} onReorder={onReorder}>
      {(s, drag) => row(s, showProject, drag)}
    </SortableRows>
  );

  // A section folds like a project group and leaves with its last thread.
  const section = ({ key, name, threads, showProject }: SidebarSection & { key: string }) => (
    <ProjectGroup
      key={key}
      name={name}
      count={threads.length}
      newCount={newCount(threads, activeId)}
      folded={projectView.collapsed.has(key)}
      // The last thread in a group is taking the group with it. Without
      // this the row folds away and the header snaps out from under it a
      // frame later; with it they leave together.
      leaving={threads.length === 1 && threads[0].id === exiting?.id}
      onToggle={() => projectView.onToggleCollapse(key)}
    >
      {run(threads, showProject)}
    </ProjectGroup>
  );

  // The bare list has no header: it is the only section there is.
  const [first] = sections;
  if (first.key === null) return run(first.threads, first.showProject);
  return <>{sections.map((s) => s.key !== null && section({ ...s, key: s.key }))}</>;
}

/**
 * The way into the palette, which is also how a phone searches threads. A
 * search-box shape that takes the row's spare width on desktop, with ⌘K in
 * its corner where hints are shown; a thumb-sized icon on a phone, where the
 * row has none to spare beside the other three.
 */
function PaletteButton({ onOpen }: { onOpen: () => void }) {
  const chord = useShortcut("palette");
  return (
    <button
      type="button"
      onClick={onOpen}
      aria-label="Search threads and commands"
      // Shrinks before anything else in the row does: the chip clips at the
      // narrowest sidebar rather than pushing the icons out of it.
      className="text-muted-foreground hover:text-foreground hover:bg-accent dark:hover:bg-accent/50 focus-visible:ring-ring/50 flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-md outline-none focus-visible:ring-[3px] md:h-8 md:w-auto md:min-w-0 md:flex-1 md:shrink md:justify-start md:gap-1.5 md:overflow-hidden md:px-2"
    >
      <SearchIcon aria-hidden className="size-4 shrink-0" />
      <span className="hidden truncate text-[13px] md:inline">Search</span>
      {chord && (
        <KbdChord
          shortcut={chord.shortcut}
          platform={chord.platform}
          className="ml-auto hidden shrink-0 md:inline-flex"
        />
      )}
    </button>
  );
}

function SidebarPanel({
  showCollapse,
  flow,
  ...props
}: SidebarProps & {
  showCollapse: boolean;
  flow: DeleteFlow;
}) {
  const { projectView } = props;
  // Reorder mode is this panel's alone: closing the sheet ends it, so the
  // next time the list opens its rows are for opening threads again.
  const [reordering, setReordering] = useState(false);
  // The footer's job is to admit that threads are missing.
  const shownCount = visibleByProject(props.threads, props.projects, projectView.hidden).length;
  return (
    <div className="bg-sidebar text-sidebar-foreground flex h-full min-h-0 flex-col">
      {/* Two rows. The top is the wordmark and the way to put it away,
          in the corner it always occupies. The row under it is what you do
          with the list: start a thread, start a project, choose what shows.
          Icons only: the list below already says what it is. */}
      <div className="flex flex-col gap-0.5 px-3 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-1.5">
        <div className="flex min-h-11 items-center gap-2 md:min-h-8">
          <span className="flex flex-1 items-center px-1.5">
            <Wordmark className="h-[18px] w-auto" />
          </span>
          {showCollapse && (
            <IconButton
              label="Hide threads"
              shortcut="toggleSidebar"
              onClick={() => props.onOpenChange(false)}
              className="text-muted-foreground hover:text-foreground"
            >
              {/* A notch under the actions: it is about the panel, not the
                  list, and should not compete with them. */}
              <PanelLeftIcon className="size-3.5" />
            </IconButton>
          )}
        </div>
        <div className="flex items-center justify-end gap-1">
          {/* While reordering it is the only thing in this row — a phone-width
              sheet has no room for a hint, a Done and four icons — and the
              way out is a word, not an icon, so it cannot be missed. */}
          {reordering ? (
            <>
              <span className="text-muted-foreground mr-auto px-1.5 text-[12px]">
                Drag the grips to reorder
              </span>
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setReordering(false)}
                className="h-11 px-4 md:h-8"
              >
                Done
              </Button>
            </>
          ) : (
            <>
              <PaletteButton onOpen={props.onOpenPalette} />
              {/* Asks where first; shift skips the question for a draft in
                  the project already open. */}
              <IconButton
                label="New thread"
                shortcut="newThreadIn"
                onClick={(e) => (e.shiftKey ? props.onNew() : props.onNewThreadIn())}
                className="text-muted-foreground hover:text-foreground"
              >
                <SquarePenIcon />
              </IconButton>
              <IconButton
                label="New project"
                onClick={props.onNewProject}
                className="text-muted-foreground hover:text-foreground"
              >
                <FolderPlusIcon />
              </IconButton>
              <ThreadFilter
                grouped={projectView.grouped}
                onToggleGrouped={projectView.onToggleGrouped}
                projects={props.projects}
                hiddenProjects={projectView.hidden}
                onToggleProject={projectView.onToggle}
                onShowAllProjects={projectView.onShowAll}
                onHideAllProjects={projectView.onHideAll}
                onReorder={() => setReordering(true)}
              />
            </>
          )}
        </div>
      </div>

      <nav
        aria-label="Threads"
        // The scroller a drag nudges along when the row nears an edge.
        data-reorder-scroll
        className="scroll-thin min-h-0 flex-1 overflow-y-auto px-2 py-2"
      >
        <ThreadList
          flow={flow}
          activeId={props.activeId}
          onSelect={props.onSelect}
          accentOf={props.accentOf}
          projects={props.projects}
          projectName={props.projectName}
          projectView={projectView}
          onSetPinned={props.onSetPinned}
          onSetUnread={props.onSetUnread}
          onRename={props.onRename}
          onCopyLink={props.onCopyLink}
          onReorder={props.onReorder}
          reordering={reordering}
          showJumpHints={props.showJumpHints}
        />
      </nav>

      <Separator />

      {/* From md up the first three hug their 24px glyphs. A touch screen
          that wide (an iPad) gets 44px tall targets back as hit area, 40px
          wide: the 8px gaps are all there is between them. */}
      <div className="flex items-center gap-2 px-3 py-2 pb-[calc(0.5rem+env(safe-area-inset-bottom))]">
        {/* Filtered, the count says so: with grouping gone there is nothing
            else on screen to admit that threads are missing. */}
        <span className="text-muted-foreground flex-1 text-[11px]">
          {shownCount < props.threads.length
            ? `${shownCount} of ${props.threads.length} threads`
            : `${props.threads.length} thread${props.threads.length === 1 ? "" : "s"}`}
        </span>
        {/* The account-level Usage page: one tap from wherever the list
            already is, and reachable with nothing open. */}
        <Tooltip>
          <TooltipTrigger asChild>
            <IconButton
              label="Usage and limits"
              onClick={props.onShowUsage}
              className="text-muted-foreground hover:text-foreground hit-44 relative [--hit-w:40px] md:size-6"
            >
              <ActivityIcon />
            </IconButton>
          </TooltipTrigger>
          <TooltipContent>Usage and limits</TooltipContent>
        </Tooltip>
        {/* Skills, MCP servers and sign-ins belong to the user and the project
            before they belong to a thread, so the page is reachable from here
            with nothing open. */}
        <IconButton
          label="Skills, MCP and sign-ins"
          onClick={props.onShowSkills}
          className="text-muted-foreground hover:text-foreground hit-44 relative [--hit-w:40px] md:size-6"
        >
          <BlocksIcon />
        </IconButton>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              onClick={props.onShowAccess}
              aria-label="How to reach this server"
              // The dot stays a dot; the target around it is thumb-sized on a
              // phone and shrinks to the dot again from md up, where touch
              // keeps a thumb's target as hit area.
              className="focus-visible:ring-ring hit-44 relative flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full [--hit-w:40px] outline-none focus-visible:ring-2 md:size-6"
            >
              <StatusDot status={props.status} />
            </button>
          </TooltipTrigger>
          <TooltipContent>How to reach this server</TooltipContent>
        </Tooltip>
        <IconButton label="Settings" shortcut="settings" onClick={props.onShowSettings} className="hit-44 relative">
          <SettingsIcon />
        </IconButton>
        <ThemeToggle />
      </div>
    </div>
  );
}

// Memoised, as its rows are: App re-renders on every frame the server sends and
// every key the composer takes, and almost none of them touch the list. App
// hands it nothing but values and callbacks that keep their identity.
export const Sidebar = memo(function Sidebar(props: SidebarProps) {
  const isDesktop = useIsDesktop();
  // Held here, above both shapes, so a delete survives the sheet closing under
  // it and the switch between them. The dialog is rendered here for the same
  // reason: selecting a thread closes the sheet on a phone, and deleting one
  // selects it.
  const flow = useDeleteFlow(props);
  // App only asks for the chips where the digits work; a phone or a tablet
  // with no pointer gets none, like every other hint.
  const showJumpHints = useShowShortcuts() && props.showJumpHints;

  // Below md the sidebar is a drawer over the transcript, which is a sheet's
  // whole job: overlay, focus trap, escape to close. At md it is the docked
  // panel again and collapses by margin, exactly as before — the breakpoint
  // here is the same one useMediaQuery and the CSS agree on.
  if (!isDesktop) {
    return (
      <>
        <Sheet open={props.open} onOpenChange={props.onOpenChange} backDismiss={props.backCloses}>
          <SheetContent
            // A dialog to Radix, but the app's own chrome to the user: the
            // shortcuts keep working while it is open.
            {...KEYBINDINGS_PASS}
            side="left"
            tabIndex={-1}
            // Full-bleed on a phone. A 15% sliver of dimmed transcript is not
            // context, it is a target for a mis-tap, and with no thread
            // selected there is nothing behind the panel at all.
            // `sm:max-w-none` is not redundant: the sheet's own base classes cap
            // it at 24rem from `sm` up, which would leave a 384px panel on a
            // landscape phone — inside this branch, but past that breakpoint.
            className="w-screen max-w-none gap-0 border-r-0 p-0 pl-[env(safe-area-inset-left)] sm:max-w-none"
            // The sheet's own X would be a second close control in the same
            // corner as the collapse button, misaligned with it and present
            // even when there is nothing to close back to. One control, and it
            // lives in the panel header where the docked sidebar puts it.
            showCloseButton={false}
            // Radix otherwise focuses the first control inside, which pops its
            // tooltip open on a touch screen and leaves it there. Focus still
            // has to enter the panel — a modal that traps focus outside itself
            // is unusable with a keyboard or a screen reader — so it lands on
            // the panel rather than nowhere.
            onOpenAutoFocus={(e) => {
              e.preventDefault();
              (e.currentTarget as HTMLElement | null)?.focus();
            }}
            // Escape in a row's rename field means "keep the old name", not
            // "close the drawer". Radix hears the key on the document before
            // the field does, so the field cannot stop it on its own.
            onEscapeKeyDown={(e) => {
              if (isTitleEditor(document.activeElement)) e.preventDefault();
            }}
          >
            <SheetTitle className="sr-only">Threads</SheetTitle>
            {/* Nothing behind the panel means nothing to collapse to. */}
            <SidebarPanel
              {...props}
              showJumpHints={showJumpHints}
              flow={flow}
              showCollapse={props.activeId !== null}
            />
          </SheetContent>
        </Sheet>
        <DeleteThreadDialog flow={flow.thread} />
      </>
    );
  }

  return (
    <>
      <DockedSidebar {...props} showJumpHints={showJumpHints} flow={flow} />
      <DeleteThreadDialog flow={flow.thread} />
    </>
  );
});

function DockedSidebar({
  flow,
  ...props
}: SidebarProps & {
  flow: DeleteFlow;
}) {
  const [width, setWidth] = useState(() => {
    const stored = Number(localStorage.getItem(WIDTH_KEY));
    return Number.isFinite(stored) && stored >= MIN_WIDTH && stored <= MAX_WIDTH
      ? stored
      : DEFAULT_WIDTH;
  });
  const dragging = useRef(false);
  // The drag handlers close over nothing but this ref, so the release handler
  // can persist the final width without reaching into React state.
  const widthRef = useRef(width);
  // Resizing must not animate: the margin transition exists for open/close,
  // and fighting the pointer with a 200ms lag makes the drag feel broken.
  const [resizing, setResizing] = useState(false);

  useEffect(() => {
    widthRef.current = width;
    if (!dragging.current) localStorage.setItem(WIDTH_KEY, String(width));
  }, [width]);

  const startDrag = useCallback((e: ReactPointerEvent) => {
    e.preventDefault();
    dragging.current = true;
    setResizing(true);
    const onMove = (m: PointerEvent) => {
      const w = Math.min(MAX_WIDTH, Math.max(MIN_WIDTH, m.clientX));
      widthRef.current = w;
      setWidth(w);
    };
    const onUp = () => {
      dragging.current = false;
      setResizing(false);
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      localStorage.setItem(WIDTH_KEY, String(widthRef.current));
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  }, []);

  return (
    <aside
      // Collapsed it is off-screen but still in the document, so it is taken
      // out of the tab order rather than being a set of controls you can focus
      // but not see.
      inert={!props.open}
      style={{ width, marginLeft: props.open ? 0 : -width }}
      className={cn(
        "relative shrink-0 border-r",
        !resizing && "transition-[margin] duration-200 motion-reduce:transition-none",
      )}
    >
      <SidebarPanel {...props} flow={flow} showCollapse />
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize the sidebar"
        onPointerDown={startDrag}
        // z-20: the handle overhangs 4px into <main>, whose header/composer
        // fade gradients are z-10 and painted later in the DOM — at equal
        // z-index they'd carve a notch out of the hover highlight.
        className="hover:bg-primary/40 absolute inset-y-0 -right-1 z-20 w-2 cursor-col-resize"
      />
    </aside>
  );
}
