import { ChevronRightIcon, CircleAlertIcon, FolderIcon, GitBranchIcon, PanelLeftIcon, PlusIcon, TagIcon, XIcon } from "lucide-react";
import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react";

import type { ConnectionStatus } from "~/client";
import {
  DeleteSessionDialog,
  useDeleteSession,
} from "~/components/DeleteSessionDialog";
import { HarnessBadge } from "~/components/HarnessBadge";
import { IconButton } from "~/components/IconButton";
import { LabelDot, LabelMenu } from "~/components/LabelMenu";
import {
  DropdownMenu,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { StatusDot } from "~/components/StatusDot";
import { ThemeToggle } from "~/components/ThemeToggle";
import { Button } from "~/components/ui/button";
import { Separator } from "~/components/ui/separator";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import { Tooltip, TooltipContent, TooltipTrigger } from "~/components/ui/tooltip";
import { cn } from "~/lib/utils";
import type { Label, SessionMeta } from "~/protocol";
import { buildGroups } from "~/sidebarGroups";
import { useIsDesktop } from "~/useMediaQuery";

const BUSY_PHASES = ["turn", "provisioning", "creating", "cleaning"];
// How long a row takes to fold away once it has left the list. Kept in step
// with the duration on the row itself.
const EXIT_MS = 260;
const FAILED_PHASES = ["provision_failed", "cleanup_failed"];

// The server derives attention from the live projection, which knows about
// pending permissions and questions; phase alone does not. The phase sets
// above remain only as a fallback for a server that predates attention.
function working(s: SessionMeta) {
  return s.attention ? s.attention === "working" : BUSY_PHASES.includes(s.phase);
}
function needsInput(s: SessionMeta) {
  return s.attention === "needs_permission" || s.attention === "needs_answer";
}
function failed(s: SessionMeta) {
  return s.attention ? s.attention === "failed" : FAILED_PHASES.includes(s.phase);
}

function ago(ms: number) {
  const s = Math.max(0, Math.floor((Date.now() - ms) / 1000));
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

const WIDTH_KEY = "omniplex.sidebarWidth";
// Which label groups this device has folded shut. Deliberately device-local,
// like the width: only the per-label "collapsed by default" flag syncs, so a
// phone and a desktop can hold different groups open.
const COLLAPSE_KEY = "omniplex.labelCollapse";

function loadCollapse(): Record<string, boolean> {
  try {
    const raw = JSON.parse(localStorage.getItem(COLLAPSE_KEY) ?? "{}");
    return raw && typeof raw === "object" ? (raw as Record<string, boolean>) : {};
  } catch {
    return {};
  }
}
const MIN_WIDTH = 208;
const MAX_WIDTH = 480;
const DEFAULT_WIDTH = 288;

interface SidebarProps {
  sessions: SessionMeta[];
  activeId: string | null;
  status: ConnectionStatus;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSelect: (id: string) => void;
  onNew: () => void;
  /**
   * removeWorktree is the user's answer to the dialog's checkbox, never
   * inferred. The promise, if one is returned, only says the request was
   * accepted — the delete is finished when the session leaves `sessions`.
   */
  onDelete: (id: string, removeWorktree: boolean) => void | Promise<unknown>;
  /** Opens the "how to reach this server" panel. */
  onShowAccess: () => void;
  // Supplied by the server via the adapter; the sidebar knows no harness names.
  accentOf: (harness: string) => string | undefined;
  projectName: (id?: string) => string | undefined;
  /** The project's own checkout, which is never a worktree omniplex may remove. */
  projectRoot: (id?: string) => string | undefined;
  /**
   * The user's label definitions, in their chosen order. Empty means the
   * feature is un-opted-into and the list renders exactly as it always has.
   */
  labels: Label[];
  /** Files a session under a label; "" clears it. */
  onSetLabel: (sessionId: string, labelId: string) => void;
  /** Opens the label manager, which App owns — the header can open it too. */
  onManageLabels: () => void;
}

/**
 * Everything the delete takes: the confirmation, the wait, and the row's exit.
 *
 * It lives above the sidebar's two shapes rather than inside the list, because
 * the list does not outlive either of them. The mobile sheet closes when a
 * session is selected — which deleting a row does — and crossing the `md`
 * breakpoint swaps the sheet for the docked panel outright. Both unmount the
 * list, and a delete held in there would lose its dialog, its ordering and its
 * animation mid-flight.
 */
function useDeleteFlow({
  sessions,
  onDelete,
  projectRoot,
}: Pick<SidebarProps, "sessions" | "onDelete" | "projectRoot">) {
  // Two pieces of state, and both are about the *list* — the confirmation, the
  // guards and the wait all live in useDeleteSession, which the transcript's
  // "this landed" prompt opens too.
  //
  // `frozen` pins the list to the order it had when Delete was pressed: the
  //   server stamps the session as it enters "cleaning" and the list is
  //   ordered by that stamp, so without this the row shoots to the top and
  //   sits there until it vanishes.
  // `exiting` keeps the row on screen, in its own place, for one last
  //   animation after it has already left the list.
  const [frozen, setFrozen] = useState<string[] | null>(null);
  const [exiting, setExiting] = useState<SessionMeta | null>(null);

  const session = useDeleteSession({
    sessions,
    onDelete,
    projectRoot,
    onStart: () => {
      setFrozen(sessions.map((s) => s.id));
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
  const { deleting } = session;

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
    if (!frozen) return sessions;
    const rank = new Map(frozen.map((id, i) => [id, i]));
    // Anything the server has added since sorts ahead, which is where a new
    // session belongs in a most-recent-first list anyway.
    const list = [...sessions].sort((a, b) => (rank.get(a.id) ?? -1) - (rank.get(b.id) ?? -1));
    if (exiting && !sessions.some((s) => s.id === exiting.id)) {
      const at = frozen.indexOf(exiting.id);
      if (at >= 0) list.splice(Math.min(at, list.length), 0, exiting);
    }
    return list;
  }, [sessions, frozen, exiting]);

  return { session, rows, ask: session.ask, deleting, exiting };
}

type DeleteFlow = ReturnType<typeof useDeleteFlow>;

function SessionList({
  flow,
  activeId,
  onSelect,
  accentOf,
  projectName,
  labels,
  onSetLabel,
  onManageLabels,
  collapsed,
  onToggleGroup,
}: Pick<
  SidebarProps,
  "activeId" | "onSelect" | "accentOf" | "projectName" | "labels" | "onSetLabel" | "onManageLabels"
> & {
  flow: DeleteFlow;
  /** Effective per-group overrides; absence falls back to the label's default. */
  collapsed: Record<string, boolean>;
  onToggleGroup: (key: string, collapsed: boolean) => void;
}) {
  const { rows, ask, deleting, exiting } = flow;

  // No sessions is no sessions: labels are a way to arrange a list, not a
  // thing to show in place of one.
  if (rows.length === 0) {
    return (
      <p className="text-muted-foreground px-3 py-10 text-center text-[13px]">
        No sessions yet.
        <br />
        <span className="text-[12px]">Start one to see it here.</span>
      </p>
    );
  }

  // The row carries its actions as overlaid buttons rather than click handlers
  // on the container — a button cannot legally nest inside another button. The
  // delete X (and, with labels defined, the label tag beside it) overlays the
  // timestamp's corner instead of owning a column of its own, so an un-hovered
  // row has no phantom right margin; on hover (desktop) the timestamp yields.
  const row = (s: SessionMeta) => {
    const active = s.id === activeId;
    const leaving = exiting?.id === s.id;
    const going = deleting?.id === s.id;
    return (
          // The row leaves from wherever it stands: it fades and slides out
          // while its own height folds shut under it, so the rows below close
          // the gap in the same motion instead of snapping up. The height is
          // the `1fr`→`0fr` grid track, which is the one way to transition to
          // a content-sized height the row never had to declare.
          <div
            key={s.id}
            inert={leaving}
            className={cn(
              "grid transition-[grid-template-rows,opacity,transform,margin] duration-[260ms] ease-out motion-reduce:transition-none",
              leaving
                ? "mb-0 grid-rows-[0fr] -translate-x-2 scale-[0.98] opacity-0"
                : "mb-0.5 grid-rows-[1fr]",
            )}
          >
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
                <span
                  className={cn(
                    "flex items-center gap-1.5",
                    // Room for the always-visible touch controls: the X, and
                    // the label tag beside it once labels exist. On desktop the
                    // controls only exist on hover, so the line runs full width
                    // until then — but it has to yield when they arrive. The
                    // fading timestamp alone covers one control; a second one
                    // would sit on top of the title, so with labels in play the
                    // hovered line reserves the pair's full width.
                    //
                    // The open label menu is the third case: Radix moves focus
                    // into a portal, so once the pointer leaves the row neither
                    // hover nor focus-within holds, yet the trigger stays lit.
                    // `has` reads it off the trigger's own aria-expanded, which
                    // — unlike data-state — no tooltip on the same element can
                    // claim. Deliberately not transitioned: an animated padding
                    // hands the tag ~150ms sitting on the title, which is the
                    // bug in miniature. The line yields first, then it fades in.
                    labels.length > 0
                      ? "pr-16 md:pr-0 md:group-hover:pr-16 md:group-focus-within:pr-16 md:group-has-[[aria-expanded=true]]:pr-16"
                      : "pr-8 md:pr-0",
                  )}
                >
                  <span className="min-w-0 flex-1 truncate text-[13px]">
                    {s.title || "Untitled"}
                  </span>
                  {working(s) && (
                    <span
                      role="status"
                      aria-label="Working"
                      className="bg-primary size-1.5 shrink-0 animate-pulse rounded-full motion-reduce:animate-none"
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
                  <span className="text-muted-foreground shrink-0 font-mono text-[10px] transition-opacity md:group-hover:opacity-0 md:group-focus-within:opacity-0">
                    {ago(s.updatedAt)}
                  </span>
                </span>
                <span className="text-muted-foreground mt-1 flex min-w-0 items-center gap-1 font-mono text-[10px]">
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
                  <span className="ml-auto flex shrink-0 items-center pl-1.5">
                    <HarnessBadge
                      harness={s.harness}
                      accent={accentOf(s.harness)}
                      className="size-3.5"
                    />
                  </span>
                </span>
              </button>

              {labels.length > 0 && (
                <DropdownMenu>
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          aria-label={`Label session ${s.title || "Untitled"}`}
                          // Sits one control-width left of the X and reveals
                          // the same way, so the pair reads as one action rail.
                          // It also stays up while its menu is: keyed off
                          // aria-expanded, because the tooltip wrapped around
                          // this same element wins the data-state attribute and
                          // reports "closed" with the menu plainly open.
                          className="absolute top-0.5 right-8 size-8 shrink-0 after:absolute after:-inset-1.5 after:content-[''] md:size-8 md:opacity-0 md:after:hidden md:group-hover:opacity-100 md:focus-visible:opacity-100 md:aria-expanded:opacity-100"
                        >
                          <TagIcon />
                        </Button>
                      </DropdownMenuTrigger>
                    </TooltipTrigger>
                    <TooltipContent>Label session</TooltipContent>
                  </Tooltip>
                  <LabelMenu
                    labels={labels}
                    current={s.labelId}
                    onSelect={(labelId) => onSetLabel(s.id, labelId)}
                    onManage={onManageLabels}
                  />
                </DropdownMenu>
              )}

              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    variant="ghost"
                    size="icon"
                    aria-label={`Delete session ${s.title || "Untitled"}`}
                    onClick={() => ask(s)}
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
                <TooltipContent>Delete session</TooltipContent>
              </Tooltip>
            </div>
          </div>
        );
  };

  // No label in use: the flat list, exactly as it has always rendered.
  // Grouping is computed over the delete flow's rows — frozen order, exiting
  // row and all — so a departing session folds away inside its own group.
  const groups = buildGroups(rows, labels);
  if (!groups) return <>{rows.map(row)}</>;

  return (
    <>
      {groups.map((g) => {
        // The unlabelled run is not a group the user made, so it gets no
        // heading, no chevron and no count — it is just the top of the list,
        // the way it looked before any label existed.
        if (!g.label) return <Fragment key="unlabelled">{g.sessions.map(row)}</Fragment>;
        const isCollapsed = collapsed[g.label.id] ?? g.label.collapsedByDefault ?? false;
        return (
          <section key={g.label.id} aria-label={g.label.name}>
            <button
              type="button"
              onClick={() => onToggleGroup(g.label!.id, !isCollapsed)}
              aria-expanded={!isCollapsed}
              className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex w-full cursor-pointer items-center gap-1.5 rounded-md px-2 pt-2 pb-1 text-[11px] font-medium outline-none focus-visible:ring-2"
            >
              <ChevronRightIcon
                aria-hidden
                className={cn(
                  "size-3 shrink-0 transition-transform motion-reduce:transition-none",
                  !isCollapsed && "rotate-90",
                )}
              />
              <LabelDot color={g.label.color} />
              <span className="truncate">{g.label.name}</span>
              <span className="ml-auto font-mono text-[10px] tabular-nums">
                {g.sessions.length}
              </span>
            </button>
            {!isCollapsed && g.sessions.map(row)}
          </section>
        );
      })}
    </>
  );
}

function SidebarPanel({
  showCollapse,
  flow,
  collapsed,
  onToggleGroup,
  ...props
}: SidebarProps & {
  showCollapse: boolean;
  flow: DeleteFlow;
  collapsed: Record<string, boolean>;
  onToggleGroup: (key: string, collapsed: boolean) => void;
}) {
  return (
    <div className="bg-sidebar text-sidebar-foreground flex h-full min-h-0 flex-col">
      {/* One quiet header row: what the panel is, and the one action it
          offers. Branding and the status dot earn no space up here — the dot
          lives in the footer, still one click from the access panel. */}
      <div className="flex items-center gap-2 px-3 pt-[calc(0.5rem+env(safe-area-inset-top))] pb-1.5">
        <span className="flex-1 px-1.5 font-mono text-sm font-semibold tracking-tight">Omniplex</span>
        <IconButton
          label="Labels"
          onClick={props.onManageLabels}
          className="text-muted-foreground hover:text-foreground"
        >
          <TagIcon />
        </IconButton>
        <IconButton label="New session" onClick={props.onNew} className="text-muted-foreground hover:text-foreground">
          <PlusIcon />
        </IconButton>
        {showCollapse && (
          <IconButton
            label="Hide sessions"
            onClick={() => props.onOpenChange(false)}
            className="text-muted-foreground hover:text-foreground"
          >
            <PanelLeftIcon />
          </IconButton>
        )}
      </div>

      <nav aria-label="Sessions" className="scroll-thin min-h-0 flex-1 overflow-y-auto px-2 py-2">
        <SessionList
          flow={flow}
          activeId={props.activeId}
          onSelect={props.onSelect}
          accentOf={props.accentOf}
          projectName={props.projectName}
          labels={props.labels}
          onSetLabel={props.onSetLabel}
          onManageLabels={props.onManageLabels}
          collapsed={collapsed}
          onToggleGroup={onToggleGroup}
        />
      </nav>

      <Separator />

      <div className="flex items-center gap-2 px-3 py-2 pb-[calc(0.5rem+env(safe-area-inset-bottom))]">
        <span className="text-muted-foreground flex-1 text-[11px]">
          {props.sessions.length} session{props.sessions.length === 1 ? "" : "s"}
        </span>
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
        <ThemeToggle />
      </div>
    </div>
  );
}

export function Sidebar(props: SidebarProps) {
  const isDesktop = useIsDesktop();
  // Held here, above both shapes, so a delete survives the sheet closing under
  // it and the switch between them. The dialog is rendered here for the same
  // reason: selecting a session closes the sheet on a phone, and deleting one
  // selects it.
  const flow = useDeleteFlow(props);

  // Which groups this device has folded, above both shapes for the same
  // reason as the delete flow. Only explicit toggles are stored: a group the
  // user never touched keeps following its label's synced default.
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>(loadCollapse);
  const onToggleGroup = useCallback((key: string, value: boolean) => {
    setCollapsed((current) => {
      const next = { ...current, [key]: value };
      try {
        localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next));
      } catch {
        // Storage can be blocked outright; the toggle still works for this page.
      }
      return next;
    });
  }, []);

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
            // context, it is a target for a mis-tap, and with no session
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
            <SheetTitle className="sr-only">Sessions</SheetTitle>
            {/* Nothing behind the panel means nothing to collapse to. */}
            <SidebarPanel
              {...props}
              flow={flow}
              collapsed={collapsed}
              onToggleGroup={onToggleGroup}
              showCollapse={props.activeId !== null}
            />
          </SheetContent>
        </Sheet>
        <DeleteSessionDialog flow={flow.session} />
      </>
    );
  }

  return (
    <>
      <DockedSidebar {...props} flow={flow} collapsed={collapsed} onToggleGroup={onToggleGroup} />
      <DeleteSessionDialog flow={flow.session} />
    </>
  );
}

function DockedSidebar({
  flow,
  collapsed,
  onToggleGroup,
  ...props
}: SidebarProps & {
  flow: DeleteFlow;
  collapsed: Record<string, boolean>;
  onToggleGroup: (key: string, collapsed: boolean) => void;
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
        collapsed={collapsed}
        onToggleGroup={onToggleGroup}
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
