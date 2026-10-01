import {
  ActivityIcon,
  BookOpenIcon,
  FolderPlusIcon,
  SettingsIcon,
  PanelLeftIcon,
  SquarePenIcon,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";

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
import { Separator } from "~/components/ui/separator";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { visibleThreads } from "~/labelFilter";
import { cn } from "~/lib/utils";
import { groupThreads, visibleByProject } from "~/projectGroups";
import type { Label, Project, ThreadMeta } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";
import { Wordmark } from "./Logo";
import { ProjectGroup } from "./sidebar/ProjectGroup";
import { ThreadRow } from "./sidebar/ThreadRow";
import { useStoredKeys, withKey } from "./sidebar/useStoredKeys";

// How long a row takes to fold away once it has left the list. Kept in step
// with the duration on the row itself.
const EXIT_MS = 260;

const WIDTH_KEY = "omniplex.sidebarWidth";
// Which labels this device is currently hiding. Device-local like the width:
// the phone is usually filtered down to one thing and the desktop is not, and
// making that travel would mean one of them is always wrong.
const FILTER_KEY = "omniplex.labelFilter";
// Which projects this device is hiding, and which of the groups it is showing
// are folded shut. Device-local for the same reason as the label filter and
// the width: the phone is usually narrowed to the one thing being worked on
// and the desktop is not.
const PROJECT_FILTER_KEY = "omniplex.projectFilter";
const COLLAPSED_KEY = "omniplex.projectCollapsed";

/**
 * The project filter and the collapse state, threaded to the header and the
 * list together. They travel as one because they are one control surface: the
 * menu decides which groups exist and the headers decide which are open.
 */
interface ProjectView {
  /** Project ids switched off in the header menu. */
  hidden: Set<string>;
  /** Group keys folded shut. Remembered across reloads, per device. */
  collapsed: Set<string>;
  onToggle: (id: string, show: boolean) => void;
  onShowAll: () => void;
  onHideAll: () => void;
  onToggleCollapse: (key: string) => void;
}
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
  onNew: () => void;
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
   * The user's label definitions, in their chosen order. Empty means the
   * feature is un-opted-into and the list renders exactly as it always has.
   */
  labels: Label[];
  /** Files a thread under a label; "" clears it. */
  onSetLabel: (threadId: string, labelId: string) => void;
  /** Opens the label manager, which App owns — the header can open it too. */
  onManageLabels: () => void;
  /** Opens the new project dialog, which App owns. */
  onNewProject: () => void;
  /** Flips the unread flag by hand — the row's "come back to this" action. */
  onSetUnread: (threadId: string, unread: boolean) => void;
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
    // thread belongs in a newest-created-first list anyway.
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
  labels,
  onSetLabel,
  onManageLabels,
  onSetUnread,
  hidden,
  onShowAll,
}: Pick<
  SidebarProps,
  | "activeId"
  | "onSelect"
  | "accentOf"
  | "projects"
  | "projectName"
  | "labels"
  | "onSetLabel"
  | "onManageLabels"
  | "onSetUnread"
> & {
  flow: DeleteFlow;
  /** Filter keys switched off in the header menu: label ids, and `UNLABELLED`. */
  hidden: Set<string>;
  projectView: ProjectView;
  onShowAll: () => void;
}) {
  const { rows, ask, deleting, exiting } = flow;

  // Both filters run over the delete flow's rows — frozen order, exiting row
  // and all — so a departing thread folds away in place instead of vanishing
  // the instant a filter is recomputed. Grouping runs over the result for the
  // same reason: it preserves order within a group, so the row still leaves
  // from exactly where it stood.
  const shown = visibleByProject(
    visibleThreads(rows, labels, hidden),
    projects,
    projectView.hidden,
  );
  const groups = groupThreads(shown, projects);
  // One group is not a grouping, however it came to be the only one.
  const grouped = groups.length > 1;

  // No threads is no threads: labels are a way to narrow a list, not a
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
  if (shown.length === 0) {
    return (
      <div className="px-3 py-10 text-center">
        <p className="text-muted-foreground text-[13px]">
          {rows.length} thread{rows.length === 1 ? "" : "s"} hidden by the filters.
        </p>
        {/* Both, because the message cannot know which one emptied the list
            and hunting through two menus to find out is the thing this button
            exists to save. */}
        <Button
          variant="outline"
          size="sm"
          onClick={() => {
            onShowAll();
            projectView.onShowAll();
          }}
          className="mt-3 h-8"
        >
          Show all
        </Button>
      </div>
    );
  }

  const row = (s: ThreadMeta, showProject: boolean) => (
    <ThreadRow
      key={s.id}
      s={s}
      showProject={showProject}
      active={s.id === activeId}
      leaving={exiting?.id === s.id}
      going={deleting?.id === s.id}
      labels={labels}
      accentOf={accentOf}
      projectName={projectName}
      onSelect={onSelect}
      onSetLabel={onSetLabel}
      onManageLabels={onManageLabels}
      onSetUnread={onSetUnread}
      onDelete={ask}
    />
  );

  // One project on screen has nothing to group: the header would name the
  // only thing there is, on every row, forever.
  if (!grouped) return <>{shown.map((s) => row(s, true))}</>;

  return (
    <>
      {groups.map((g) => (
        <ProjectGroup
          key={g.key}
          name={g.name}
          count={g.threads.length}
          folded={projectView.collapsed.has(g.key)}
          // The last thread in a group is taking the group with it. Without
          // this the row folds away and the header snaps out from under it a
          // frame later; with it they leave together.
          leaving={g.threads.length === 1 && g.threads[0].id === exiting?.id}
          onToggle={() => projectView.onToggleCollapse(g.key)}
        >
          {g.threads.map((c) => row(c, false))}
        </ProjectGroup>
      ))}
    </>
  );
}

function SidebarPanel({
  showCollapse,
  flow,
  hidden,
  projectView,
  onToggleLabel,
  onShowAll,
  ...props
}: SidebarProps & {
  showCollapse: boolean;
  flow: DeleteFlow;
  hidden: Set<string>;
  projectView: ProjectView;
  onToggleLabel: (key: string, show: boolean) => void;
  onShowAll: () => void;
}) {
  // Both filters, because the footer's job is to admit that threads are
  // missing and it cannot know which control removed them.
  const shownCount = visibleByProject(
    visibleThreads(props.threads, props.labels, hidden),
    props.projects,
    projectView.hidden,
  ).length;
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
          <IconButton
            label="New thread"
            onClick={props.onNew}
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
            projects={props.projects}
            hiddenProjects={projectView.hidden}
            onToggleProject={projectView.onToggle}
            onShowAllProjects={projectView.onShowAll}
            onHideAllProjects={projectView.onHideAll}
            labels={props.labels}
            hiddenLabels={hidden}
            onToggleLabel={onToggleLabel}
            onShowAllLabels={onShowAll}
            onManageLabels={props.onManageLabels}
          />
        </div>
      </div>

      <nav aria-label="Threads" className="scroll-thin min-h-0 flex-1 overflow-y-auto px-2 py-2">
        <ThreadList
          flow={flow}
          activeId={props.activeId}
          onSelect={props.onSelect}
          accentOf={props.accentOf}
          projects={props.projects}
          projectName={props.projectName}
          projectView={projectView}
          labels={props.labels}
          onSetLabel={props.onSetLabel}
          onManageLabels={props.onManageLabels}
          onSetUnread={props.onSetUnread}
          hidden={hidden}
          onShowAll={onShowAll}
        />
      </nav>

      <Separator />

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
              className="text-muted-foreground hover:text-foreground size-6"
            >
              <ActivityIcon />
            </IconButton>
          </TooltipTrigger>
          <TooltipContent>Usage and limits</TooltipContent>
        </Tooltip>
        {/* Skills belong to the user and the project before they belong to a
            thread, so the page is reachable from here with nothing open. */}
        <IconButton
          label="Skills"
          onClick={props.onShowSkills}
          className="text-muted-foreground hover:text-foreground md:size-6"
        >
          <BookOpenIcon />
        </IconButton>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              onClick={props.onShowAccess}
              aria-label="How to reach this server"
              // The dot stays a dot; the target around it is thumb-sized on a
              // phone and shrinks to the dot again for a pointer.
              className="focus-visible:ring-ring flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-full outline-none focus-visible:ring-2 md:size-6"
            >
              <StatusDot status={props.status} />
            </button>
          </TooltipTrigger>
          <TooltipContent>How to reach this server</TooltipContent>
        </Tooltip>
        <IconButton label="Settings" onClick={props.onShowSettings}>
          <SettingsIcon />
        </IconButton>
        <ThemeToggle />
      </div>
    </div>
  );
}

export function Sidebar(props: SidebarProps) {
  const isDesktop = useIsDesktop();
  // Held here, above both shapes, so a delete survives the sheet closing under
  // it and the switch between them. The dialog is rendered here for the same
  // reason: selecting a thread closes the sheet on a phone, and deleting one
  // selects it.
  const flow = useDeleteFlow(props);

  // Which labels this device is hiding, above both shapes for the same reason
  // as the delete flow. Only what the user switched off is stored, so a label
  // created later — here or on a paired device — arrives showing.
  const [hidden, setHidden] = useStoredKeys(FILTER_KEY);
  const onToggleLabel = useCallback(
    (key: string, show: boolean) => setHidden((current) => withKey(current, key, !show)),
    [setHidden],
  );
  const onShowAll = useCallback(() => setHidden(new Set()), [setHidden]);

  // The project filter and the fold state of the groups it produces. Same
  // reasoning as the label filter above: held over both shapes, and only the
  // user's own choices are stored, so a project added on a paired device
  // arrives showing rather than pre-hidden.
  const [hiddenProjects, setHiddenProjects] = useStoredKeys(PROJECT_FILTER_KEY);
  const [collapsed, setCollapsed] = useStoredKeys(COLLAPSED_KEY);

  const projectView: ProjectView = {
    hidden: hiddenProjects,
    collapsed,
    onToggle: useCallback(
      (id: string, show: boolean) => setHiddenProjects((current) => withKey(current, id, !show)),
      [setHiddenProjects],
    ),
    onShowAll: useCallback(() => setHiddenProjects(new Set()), [setHiddenProjects]),
    onHideAll: useCallback(
      () => setHiddenProjects(new Set(props.projects.map((p) => p.id))),
      [props.projects, setHiddenProjects],
    ),
    onToggleCollapse: useCallback(
      (key: string) => setCollapsed((current) => withKey(current, key, !current.has(key))),
      [setCollapsed],
    ),
  };

  // Below md the sidebar is a drawer over the transcript, which is a sheet's
  // whole job: overlay, focus trap, escape to close. At md it is the docked
  // panel again and collapses by margin, exactly as before — the breakpoint
  // here is the same one useMediaQuery and the CSS agree on.
  if (!isDesktop) {
    return (
      <>
        <Sheet open={props.open} onOpenChange={props.onOpenChange}>
          <SheetContent
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
          >
            <SheetTitle className="sr-only">Threads</SheetTitle>
            {/* Nothing behind the panel means nothing to collapse to. */}
            <SidebarPanel
              {...props}
              flow={flow}
              hidden={hidden}
              projectView={projectView}
              onToggleLabel={onToggleLabel}
              onShowAll={onShowAll}
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
      <DockedSidebar
        {...props}
        flow={flow}
        hidden={hidden}
        projectView={projectView}
        onToggleLabel={onToggleLabel}
        onShowAll={onShowAll}
      />
      <DeleteThreadDialog flow={flow.thread} />
    </>
  );
}

function DockedSidebar({
  flow,
  hidden,
  projectView,
  onToggleLabel,
  onShowAll,
  ...props
}: SidebarProps & {
  flow: DeleteFlow;
  hidden: Set<string>;
  projectView: ProjectView;
  onToggleLabel: (key: string, show: boolean) => void;
  onShowAll: () => void;
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
      <SidebarPanel
        {...props}
        flow={flow}
        hidden={hidden}
        projectView={projectView}
        onToggleLabel={onToggleLabel}
        onShowAll={onShowAll}
        showCollapse
      />
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
