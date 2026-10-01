import {
  BotIcon,
  FileDiffIcon,
  FileIcon,
  FolderTreeIcon,
  Maximize2Icon,
  Minimize2Icon,
  PackageIcon,
  PlugIcon,
  PlusIcon,
  TerminalIcon,
  XIcon,
} from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

import { ArtefactList } from "~/components/artefacts/ArtefactList";
import { ArtefactSurface } from "~/components/artefacts/ArtefactSurface";
import { IconButton } from "~/components/IconButton";

import { DiffSurface } from "~/components/panel/DiffSurface";
import { FileBrowser } from "~/components/panel/FileBrowser";
import { JobsSurface } from "~/components/panel/JobsSurface";
import { McpSurface } from "~/components/panel/McpSurface";
import { TerminalSurface } from "~/components/panel/TerminalSurface";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import { fileIconFor } from "~/lib/fileIcons";
import { liveJobCount } from "~/lib/jobs";
import type { Artefact } from "~/lib/artefacts";
import {
  artefactSurface,
  closeSurface,
  fileSurface,
  loadPanel,
  newTerminalSurface,
  openSurface,
  putSurface,
  savePanel,
  type PanelState,
  type Surface,
} from "~/lib/panel";
import { fileName } from "~/lib/tree";
import { cn } from "~/lib/utils";
import type { DiffComparison, FileContent, FileDiff, FileTree, PullRequest, ThreadChanges, ThreadState } from "~/protocol";
import { useLatest } from "~/useLatest";
import { useDocksPanel } from "~/useMediaQuery";

const NO_ARTEFACTS: Artefact[] = [];

const WIDTH_KEY = "omniplex.changesWidth";
const MIN_WIDTH = 320;
const DEFAULT_WIDTH = 460;

/** An imperative ask from outside: put this on screen. */
export interface PanelRequest {
  kind: "diff" | "path" | "jobs" | "artefact" | "artefacts";
  path?: string;
  line?: number;
  artefactId?: string;
  nonce: number;
}

export interface PanelProps {
  threadId: string;
  /** The thread, for the jobs surface (jobs + items) and its badge. */
  state: ThreadState;
  /** A ws command, for stopping a job and tailing a shell's output. */
  command: (command: string, args: unknown) => Promise<any>;
  open: boolean;
  onClose: () => void;
  /** One click to the full content width, one click back. No in-between. */
  expanded?: boolean;
  onToggleExpanded?: () => void;
  /** Data is re-read when this changes — when a turn ends, in practice. */
  revision: string;
  loadChanges: (comparison: DiffComparison) => Promise<ThreadChanges>;
  loadDiff: (path: string, changes: ThreadChanges) => Promise<FileDiff>;
  loadTree: (includeIgnored: boolean) => Promise<FileTree>;
  loadFile: (path: string) => Promise<FileContent>;
  request?: PanelRequest | null;
  pr?: PullRequest | null;
  /** Open Settings → Connections on an MCP server, to sign in to it. */
  onOpenConnections?: (server: string) => void;
}

function surfaceLabel(s: Surface, artefacts: Artefact[]): string {
  switch (s.kind) {
    case "artefacts":
      return "Artefacts";
    case "mcp":
      return "MCP";
    case "artefact":
      return artefacts.find((a) => a.id === s.artefactId)?.name ?? "Artefact";
    case "diff":
      return "Diff";
    case "files":
      return "Files";
    case "jobs":
      return "Jobs";
    case "terminal":
      return `Term ${s.id.slice("terminal:".length)}`;
    case "file":
      return fileName(s.path ?? "");
  }
}

/** What routing a request does to the panel. An empty route does nothing. */
interface Route {
  update?: (p: PanelState) => PanelState;
  /** A changed file to open in the diff surface. */
  reveal?: { path: string; nonce: number };
  /** Set only when a file tab opens: the line to scroll to, if any. */
  file?: { line?: number };
}

/**
 * Where a request lands, or null while it cannot be judged yet. A path request
 * routes on the change list, so it waits for the list to settle rather than
 * judging against the empty one a fresh mount holds.
 */
function routeRequest(
  request: PanelRequest,
  settled: boolean,
  changedPaths: Set<string>,
  tree: FileTree | null,
): Route | null {
  const { path, nonce } = request;
  switch (request.kind) {
    case "diff":
      return {
        update: (p) => openSurface(p, { id: "diff", kind: "diff" }),
        reveal: path ? { path, nonce } : undefined,
      };
    case "jobs":
    case "artefacts": {
      const kind = request.kind;
      return { update: (p) => openSurface(p, { id: kind, kind }) };
    }
    case "artefact": {
      const id = request.artefactId;
      return id ? { update: (p) => putSurface(p, artefactSurface(id)) } : {};
    }
  }
  if (!path) return {};
  if (!settled) return null;
  // A path in the change list opens as its diff; anything else opens as the
  // file itself, including files the thread never touched.
  if (changedPaths.has(path) && request.line === undefined) {
    return { update: (p) => openSurface(p, { id: "diff", kind: "diff" }), reveal: { path, nonce } };
  }
  // A directory reference opens the tree rather than a file that isn't one.
  if (tree?.files.some((f) => f.startsWith(path + "/")) && !tree.files.includes(path)) {
    return { update: (p) => openSurface(p, { id: "files", kind: "files" }) };
  }
  return { update: (p) => openSurface(p, fileSurface(path)), file: { line: request.line } };
}

function SurfaceIcon({ s, className }: { s: Surface; className?: string }) {
  switch (s.kind) {
    case "diff":
      return <FileDiffIcon className={className} />;
    case "files":
      return <FolderTreeIcon className={className} />;
    case "jobs":
      return <BotIcon className={className} />;
    case "artefacts":
      return <PackageIcon className={className} />;
    case "artefact":
      return <FileIcon className={className} />;
    case "mcp":
      return <PlugIcon className={className} />;
    case "terminal":
      return <TerminalIcon className={className} />;
    case "file": {
      const { Icon, tone } = fileIconFor(s.path ?? "");
      return <Icon className={cn(className, tone)} />;
    }
  }
}

function PanelBody({
  threadId,
  state,
  command,
  open,
  onClose,
  expanded: panelExpanded,
  onToggleExpanded,
  revision,
  loadChanges,
  loadDiff,
  loadTree,
  loadFile,
  request,
  pr,
  onOpenConnections,
  inSheet,
}: PanelProps & { inSheet?: boolean }) {
  // The tab model, persisted per thread so the panel reopens as it was left.
  const [panel, setPanel] = useState<PanelState>(() => loadPanel(threadId));
  useEffect(() => savePanel(threadId, panel), [threadId, panel]);

  // ---- the change list, owned here because routing needs it too ----
  const [changes, setChanges] = useState<ThreadChanges | null>(null);
  const [comparison, setComparison] = useState<DiffComparison>(() => {
    const stored = localStorage.getItem(`omniplex.diffComparison:${threadId}`);
    return stored === "branch" || stored === "pull_request" ? stored : "uncommitted";
  });
  const [changesLoading, setChangesLoading] = useState(false);
  const [changesError, setChangesError] = useState("");
  // A refresh that started earlier must not overwrite a later one's answer.
  const changesGeneration = useRef(0);
  // The loaders are held by ref: a parent that re-creates them on every render
  // must not turn "read the worktree once" into a loop.
  const loadChangesRef = useLatest(loadChanges);

  const refreshChanges = useCallback(async () => {
    const generation = ++changesGeneration.current;
    setChangesLoading(true);
    setChangesError("");
    try {
      const next = await loadChangesRef.current(comparison);
      if (generation !== changesGeneration.current) return;
      setChanges(next);
    } catch (e) {
      if (generation !== changesGeneration.current) return;
      setChangesError(e instanceof Error ? e.message : String(e));
    } finally {
      if (generation === changesGeneration.current) setChangesLoading(false);
    }
  }, [comparison, loadChangesRef]);

  const changeComparison = useCallback((next: DiffComparison) => {
    localStorage.setItem(`omniplex.diffComparison:${threadId}`, next);
    setChanges(null);
    setChangesError("");
    setComparison(next);
  }, [threadId]);

  // Opening reads the worktree, and so does the end of a turn: the agent has
  // just stopped writing, which is exactly when the data is worth re-reading.
  // A hidden panel (kept mounted so its terminals survive) reads nothing.
  useEffect(() => {
    if (open) void refreshChanges();
  }, [open, revision, refreshChanges]);

  // ---- the worktree tree, shared by the files and file surfaces ----
  const [tree, setTree] = useState<FileTree | null>(null);
  const [treeLoading, setTreeLoading] = useState(false);
  const [treeError, setTreeError] = useState("");
  const [includeIgnored, setIncludeIgnored] = useState(false);
  const treeGeneration = useRef(0);
  const treeWanted = useRef(false);
  const loadTreeRef = useLatest(loadTree);

  const refreshTree = useCallback(async (ignored?: boolean) => {
    treeWanted.current = true;
    const generation = ++treeGeneration.current;
    setTreeLoading(true);
    setTreeError("");
    try {
      const next = await loadTreeRef.current(ignored ?? false);
      if (generation !== treeGeneration.current) return;
      setTree(next);
    } catch (e) {
      if (generation !== treeGeneration.current) return;
      setTreeError(e instanceof Error ? e.message : String(e));
    } finally {
      if (generation === treeGeneration.current) setTreeLoading(false);
    }
  }, [loadTreeRef]);

  // Lazy: the tree is read the first time a surface needs it, then kept fresh
  // on the same cadence as the change list.
  const active = panel.surfaces.find((s) => s.id === panel.active);
  const needsTree = active?.kind === "files" || active?.kind === "file";
  useEffect(() => {
    if (open && needsTree && !treeWanted.current) void refreshTree(includeIgnored);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, needsTree]);
  useEffect(() => {
    if (open && treeWanted.current) void refreshTree(includeIgnored);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [revision]);

  const toggleIgnored = useCallback(() => {
    const next = !includeIgnored;
    setIncludeIgnored(next);
    void refreshTree(next);
  }, [includeIgnored, refreshTree]);

  const changedPaths = useMemo(() => new Set((changes?.files ?? []).map((f) => f.path)), [changes]);

  // ---- requests from outside ----
  const [reveal, setReveal] = useState<{ path: string; nonce: number } | null>(null);
  const [fileLine, setFileLine] = useState<number | undefined>(undefined);
  // Each nonce is routed exactly once, during render, so the tab it opens is
  // in the same paint as the request rather than one frame behind it.
  const [routedNonce, setRoutedNonce] = useState(0);
  if (request && request.nonce !== routedNonce) {
    const route = routeRequest(request, changes !== null || !!changesError, changedPaths, tree);
    // Null is "not settled yet": the nonce stays unrouted and the render the
    // change list arrives in looks again.
    if (route) {
      setRoutedNonce(request.nonce);
      if (route.update) setPanel(route.update);
      if (route.reveal) setReveal(route.reveal);
      if (route.file) setFileLine(route.file.line);
    }
  }

  const selectFile = useCallback((path: string) => {
    setFileLine(undefined);
    setPanel((p) => openSurface(p, fileSurface(path)));
  }, []);

  const jobCount = liveJobCount(state.jobs);
  const artefacts = state.artefacts ?? NO_ARTEFACTS;
  const openArtefact = useCallback(
    (id: string) => setPanel((p) => putSurface(p, artefactSurface(id))),
    [],
  );
  const shownArtefact = active?.kind === "artefact" ? artefacts.find((a) => a.id === active.artefactId) : undefined;

  const addSurface = useCallback((s: Surface) => setPanel((p) => openSurface(p, s)), []);

  // The active tab is always in view: opening an artefact from the transcript
  // appends a tab, and on a phone the strip is a few tabs wide.
  const tabsRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const strip = tabsRef.current;
    const tab = strip && panel.active ? strip.querySelector<HTMLElement>(`[data-surface="${CSS.escape(panel.active)}"]`) : null;
    if (!strip || !tab) return;
    const box = strip.getBoundingClientRect();
    const at = tab.getBoundingClientRect();
    if (at.left < box.left) strip.scrollLeft -= box.left - at.left;
    else if (at.right > box.right) strip.scrollLeft += at.right - box.right;
  }, [panel.active, open]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1 border-b px-1.5 pt-[calc(0.375rem+env(safe-area-inset-top))] pb-1.5">
        <div
          ref={tabsRef}
          role="tablist"
          aria-label="Panel tabs"
          className="scroll-thin flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto overscroll-x-contain"
        >
          {panel.surfaces.map((s) => {
            const selected = s.id === panel.active;
            return (
              <span
                key={s.id}
                data-surface={s.id}
                className={cn(
                  "group flex shrink-0 items-center rounded-md border text-[11.5px] transition-colors",
                  selected ? "bg-accent border-border" : "hover:bg-accent/50 border-transparent",
                )}
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  onClick={() => setPanel((p) => ({ ...p, active: s.id }))}
                  title={s.kind === "file" ? s.path : surfaceLabel(s, artefacts)}
                  className="focus-visible:ring-ring flex min-h-8 items-center gap-1.5 rounded-l-md py-1 pl-2 outline-none focus-visible:ring-2"
                >
                  <SurfaceIcon s={s} className="size-3.5 shrink-0" />
                  <span className="max-w-32 truncate">{surfaceLabel(s, artefacts)}</span>
                  {s.kind === "jobs" && jobCount > 0 && (
                    <span className="bg-primary/15 text-primary rounded-full px-1.5 text-[10px] tabular-nums">
                      {jobCount}
                    </span>
                  )}
                </button>
                <button
                  type="button"
                  aria-label={`Close ${surfaceLabel(s, artefacts)}`}
                  onClick={() => setPanel((p) => closeSurface(p, s.id))}
                  className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex min-h-8 items-center rounded-r-md py-1 pr-1.5 pl-1 opacity-60 outline-none focus-visible:ring-2 group-hover:opacity-100"
                >
                  <XIcon className="size-3" />
                </button>
              </span>
            );
          })}
        </div>

        {/* Outside the scroller: with a row of tabs, a + that scrolled away with
            them would be off screen on a phone. */}
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button
                type="button"
                aria-label="Open a surface"
                className="text-muted-foreground hover:text-foreground hover:bg-accent/50 focus-visible:ring-ring flex size-8 shrink-0 items-center justify-center rounded-md outline-none focus-visible:ring-2"
              >
                <PlusIcon className="size-4" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start">
              <DropdownMenuItem onSelect={() => addSurface({ id: "diff", kind: "diff" })}>
                <FileDiffIcon className="size-3.5" /> Diff
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => addSurface({ id: "files", kind: "files" })}>
                <FolderTreeIcon className="size-3.5" /> Files
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => addSurface({ id: "jobs", kind: "jobs" })}>
                <BotIcon className="size-3.5" /> Jobs
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => addSurface({ id: "artefacts", kind: "artefacts" })}>
                <PackageIcon className="size-3.5" /> Artefacts
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => addSurface({ id: "mcp", kind: "mcp" })}>
                <PlugIcon className="size-3.5" /> MCP
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setPanel((p) => openSurface(p, newTerminalSurface(p)))}>
                <TerminalIcon className="size-3.5" /> Terminal
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

        {!inSheet && onToggleExpanded && (
          <IconButton
            label={panelExpanded ? "Restore the panel" : "Expand to full width"}
            onClick={onToggleExpanded}
          >
            {panelExpanded ? <Minimize2Icon /> : <Maximize2Icon />}
          </IconButton>
        )}
        <IconButton label="Close the panel" onClick={onClose}>
          <XIcon />
        </IconButton>
      </div>

      <div className="min-h-0 flex-1">
        {panel.surfaces.length === 0 && (
          <div className="text-muted-foreground flex h-full flex-col items-center justify-center gap-2 px-6 text-center">
            <p className="text-[13px]">Nothing open.</p>
            <p className="text-[12px]">Add a surface with the + above.</p>
          </div>
        )}
        {active?.kind === "diff" && (
          <DiffSurface
            changes={changes}
            loading={changesLoading}
            error={changesError}
            onRefresh={() => void refreshChanges()}
            loadDiff={loadDiff}
            reveal={reveal}
            comparison={comparison}
            onComparisonChange={changeComparison}
            pr={pr}
          />
        )}
        {(active?.kind === "files" || active?.kind === "file") && (
          <FileBrowser
            tree={tree}
            loading={treeLoading}
            error={treeError}
            onRefresh={() => void refreshTree(includeIgnored)}
            includeIgnored={includeIgnored}
            onToggleIgnored={toggleIgnored}
            changedPaths={changedPaths}
            selectedPath={active.kind === "file" ? active.path : undefined}
            line={active.kind === "file" ? fileLine : undefined}
            onSelect={selectFile}
            loadFile={loadFile}
          />
        )}
        {active?.kind === "jobs" && <JobsSurface threadId={threadId} state={state} command={command} />}
        {active?.kind === "artefacts" && <ArtefactList artefacts={artefacts} onOpen={openArtefact} />}
        {active?.kind === "artefact" &&
          (shownArtefact ? (
            <ArtefactSurface
              key={shownArtefact.id}
              threadId={threadId}
              artefact={shownArtefact}
            />
          ) : (
            <div className="text-muted-foreground flex h-full items-center justify-center px-6 text-center text-[13px]">
              This artefact is not in this thread.
            </div>
          ))}
        {active?.kind === "mcp" && (
          <McpSurface threadId={threadId} command={command} onOpenConnections={onOpenConnections} />
        )}
        {/* Terminals stay mounted while inactive: unmounting one hangs up its
            shell, and a tab switch must not kill a running command. */}
        {panel.surfaces
          .filter((s) => s.kind === "terminal")
          .map((s) => (
            <div key={s.id} className={cn("h-full", s.id !== panel.active && "hidden")}>
              <TerminalSurface target={{ thread: threadId }} />
            </div>
          ))}
      </div>
    </div>
  );
}

/**
 * The right-hand panel: a tabbed surface — diff, files, jobs, terminal —
 * docked to the right on a desktop and a full-screen sheet on a phone, where a
 * squeezed side panel would leave neither the transcript nor the panel
 * readable.
 */
export function Panel(props: PanelProps) {
  const docked = useDocksPanel();
  const [width, setWidth] = useState(() => {
    const stored = Number(localStorage.getItem(WIDTH_KEY));
    return Number.isFinite(stored) && stored >= MIN_WIDTH ? stored : DEFAULT_WIDTH;
  });

  // Escape closes the docked panel. The sheet does this for itself.
  useEffect(() => {
    if (!props.open || !docked) return;
    const onKey = (e: KeyboardEvent) => {
      // A dialog, sheet, menu or popover owns Escape first; closing both at
      // once would dismiss something the user was not looking at. Radix
      // prevents the default of the Escape it dismisses a layer with.
      if (e.key !== "Escape" || e.defaultPrevented || document.querySelector("[role=dialog]")) return;
      props.onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [props.open, props.onClose, docked]);

  const startDrag = useCallback((e: ReactPointerEvent) => {
    e.preventDefault();
    // Stored once, when the drag ends, not on every pixel of it.
    let last: number | undefined;
    const max = () => Math.max(MIN_WIDTH, window.innerWidth - 360);
    const onMove = (m: PointerEvent) => {
      last = Math.min(max(), Math.max(MIN_WIDTH, window.innerWidth - m.clientX));
      setWidth(last);
    };
    const onUp = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      if (last !== undefined) localStorage.setItem(WIDTH_KEY, String(last));
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  }, []);

  if (!docked) {
    if (!props.open) return null;
    return (
      <Sheet open onOpenChange={(v) => !v && props.onClose()}>
        <SheetContent
          side="right"
          tabIndex={-1}
          className="w-full gap-0 p-0 sm:max-w-none"
          // The panel header carries its own close button, aligned with the
          // rest of the row.
          showCloseButton={false}
          // Radix otherwise focuses the first control inside, which pops its
          // tooltip open on a touch screen and leaves it there. Focus still
          // has to enter the panel, so it lands on the panel itself.
          onOpenAutoFocus={(e) => {
            e.preventDefault();
            (e.currentTarget as HTMLElement | null)?.focus();
          }}
        >
          <SheetTitle className="sr-only">Thread panel</SheetTitle>
          <PanelBody {...props} inSheet />
        </SheetContent>
      </Sheet>
    );
  }

  return (
    <aside
      // Expanded, the panel is the content area: the main column hides and
      // this fills what is left beside the sidebar.
      style={props.expanded ? undefined : { width }}
      className={cn(
        "relative flex flex-col border-l",
        props.expanded ? "min-w-0 flex-1" : "shrink-0",
        // Hidden, not unmounted: unmounting would hang up every terminal's
        // shell, and hiding the panel is not closing its tabs.
        !props.open && "hidden",
      )}
      aria-label="Thread panel"
    >
      {!props.expanded && (
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize the panel"
          onPointerDown={startDrag}
          // z-20: the handle overhangs 4px into <main>, whose fades are z-10;
          // below them the hover highlight comes out notched, as the
          // sidebar's did.
          className="hover:bg-primary/40 absolute inset-y-0 -left-1 z-20 w-2 cursor-col-resize"
        />
      )}
      <PanelBody {...props} />
    </aside>
  );
}
