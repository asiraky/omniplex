import {
  BotIcon,
  FileDiffIcon,
  FolderTreeIcon,
  Maximize2Icon,
  Minimize2Icon,
  PlusIcon,
  TerminalIcon,
  XIcon,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";

import { IconButton } from "~/components/IconButton";
import { AgentsSurface } from "~/components/panel/AgentsSurface";
import { DiffSurface } from "~/components/panel/DiffSurface";
import { FileBrowser } from "~/components/panel/FileBrowser";
import { TerminalSurface } from "~/components/panel/TerminalSurface";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import { Sheet, SheetContent, SheetTitle } from "~/components/ui/sheet";
import { fileIconFor } from "~/lib/fileIcons";
import { liveAgentCount } from "~/lib/agents";
import {
  closeSurface,
  fileSurface,
  loadPanel,
  newTerminalSurface,
  openSurface,
  savePanel,
  type PanelState,
  type Surface,
} from "~/lib/panel";
import { fileName } from "~/lib/tree";
import { cn } from "~/lib/utils";
import type { FileContent, FileDiff, FileTree, Item, SessionChanges } from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";

const WIDTH_KEY = "omniplex.changesWidth";
const MIN_WIDTH = 320;
const DEFAULT_WIDTH = 460;

/** An imperative ask from outside: put this on screen. */
export interface PanelRequest {
  kind: "diff" | "path";
  path?: string;
  line?: number;
  nonce: number;
}

export interface PanelProps {
  sessionId: string;
  /** The transcript's items, for the subagents surface and its badge. */
  items: Item[];
  open: boolean;
  onClose: () => void;
  /** One click to the full content width, one click back. No in-between. */
  expanded?: boolean;
  onToggleExpanded?: () => void;
  /** Data is re-read when this changes — when a turn ends, in practice. */
  revision: string;
  loadChanges: () => Promise<SessionChanges>;
  loadDiff: (path: string) => Promise<FileDiff>;
  loadTree: (includeIgnored: boolean) => Promise<FileTree>;
  loadFile: (path: string) => Promise<FileContent>;
  request?: PanelRequest | null;
}

function surfaceLabel(s: Surface): string {
  switch (s.kind) {
    case "diff":
      return "Diff";
    case "files":
      return "Files";
    case "agents":
      return "Agents";
    case "terminal":
      return `Term ${s.id.slice("terminal:".length)}`;
    case "file":
      return fileName(s.path ?? "");
  }
}

function SurfaceIcon({ s, className }: { s: Surface; className?: string }) {
  switch (s.kind) {
    case "diff":
      return <FileDiffIcon className={className} />;
    case "files":
      return <FolderTreeIcon className={className} />;
    case "agents":
      return <BotIcon className={className} />;
    case "terminal":
      return <TerminalIcon className={className} />;
    case "file": {
      const { Icon, tone } = fileIconFor(s.path ?? "");
      return <Icon className={cn(className, tone)} />;
    }
  }
}

function PanelBody({
  sessionId,
  items,
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
  inSheet,
}: PanelProps & { inSheet?: boolean }) {
  // The tab model, persisted per session so the panel reopens as it was left.
  const [panel, setPanel] = useState<PanelState>(() => loadPanel(sessionId));
  useEffect(() => savePanel(sessionId, panel), [sessionId, panel]);

  // ---- the change list, owned here because routing needs it too ----
  const [changes, setChanges] = useState<SessionChanges | null>(null);
  const [changesLoading, setChangesLoading] = useState(false);
  const [changesError, setChangesError] = useState("");
  const [reveal, setReveal] = useState<{ path: string; nonce: number } | null>(null);
  // A refresh that started earlier must not overwrite a later one's answer.
  const changesGen = useRef(0);
  // The loaders are held by ref: a parent that re-creates them on every render
  // must not turn "read the worktree once" into a loop.
  const loadChangesRef = useRef(loadChanges);
  loadChangesRef.current = loadChanges;

  const refreshChanges = useCallback(async () => {
    const mine = ++changesGen.current;
    setChangesLoading(true);
    setChangesError("");
    try {
      const next = await loadChangesRef.current();
      if (mine !== changesGen.current) return;
      setChanges(next);
    } catch (e) {
      if (mine !== changesGen.current) return;
      setChangesError(e instanceof Error ? e.message : String(e));
    } finally {
      if (mine === changesGen.current) setChangesLoading(false);
    }
  }, []);

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
  const treeGen = useRef(0);
  const treeWanted = useRef(false);
  const loadTreeRef = useRef(loadTree);
  loadTreeRef.current = loadTree;

  const refreshTree = useCallback(async (ignored?: boolean) => {
    treeWanted.current = true;
    const mine = ++treeGen.current;
    setTreeLoading(true);
    setTreeError("");
    try {
      const next = await loadTreeRef.current(ignored ?? false);
      if (mine !== treeGen.current) return;
      setTree(next);
    } catch (e) {
      if (mine !== treeGen.current) return;
      setTreeError(e instanceof Error ? e.message : String(e));
    } finally {
      if (mine === treeGen.current) setTreeLoading(false);
    }
  }, []);

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
    setIncludeIgnored((v) => {
      void refreshTree(!v);
      return !v;
    });
  }, [refreshTree]);

  const changedPaths = useMemo(() => new Set((changes?.files ?? []).map((f) => f.path)), [changes]);

  // ---- requests from outside ----
  const [fileLine, setFileLine] = useState<number | undefined>(undefined);
  // A path request routes on the change list, so it waits for the list to
  // settle rather than judging against the empty one a fresh mount holds.
  // Each nonce is routed exactly once.
  const routedNonce = useRef(0);
  useEffect(() => {
    if (!request || request.nonce === routedNonce.current) return;
    if (request.kind === "diff") {
      routedNonce.current = request.nonce;
      setPanel((p) => openSurface(p, { id: "diff", kind: "diff" }));
      if (request.path) setReveal({ path: request.path, nonce: request.nonce });
      return;
    }
    if (!request.path) {
      routedNonce.current = request.nonce;
      return;
    }
    // Not settled yet: leave the nonce unrouted and let the next changes
    // update re-run this effect.
    if (changes === null && !changesError) return;
    routedNonce.current = request.nonce;
    // A path in the change list opens as its diff; anything else opens as the
    // file itself — including files the session never touched.
    if (changedPaths.has(request.path) && request.line === undefined) {
      setPanel((p) => openSurface(p, { id: "diff", kind: "diff" }));
      setReveal({ path: request.path, nonce: request.nonce });
      return;
    }
    // A directory reference opens the tree rather than a file that isn't one.
    if (tree?.files.some((f) => f.startsWith(request.path + "/")) && !tree.files.includes(request.path)) {
      setPanel((p) => openSurface(p, { id: "files", kind: "files" }));
      return;
    }
    setFileLine(request.line);
    setPanel((p) => openSurface(p, fileSurface(request.path!)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [request?.nonce, changes, changesError]);

  const selectFile = useCallback((path: string) => {
    setFileLine(undefined);
    setPanel((p) => openSurface(p, fileSurface(path)));
  }, []);

  const agentCount = liveAgentCount(items);

  const addSurface = useCallback((s: Surface) => setPanel((p) => openSurface(p, s)), []);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1 border-b px-1.5 pt-[calc(0.375rem+env(safe-area-inset-top))] pb-1.5">
        <div role="tablist" aria-label="Panel tabs" className="scroll-thin flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto">
          {panel.surfaces.map((s) => {
            const selected = s.id === panel.active;
            return (
              <span
                key={s.id}
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
                  title={s.kind === "file" ? s.path : surfaceLabel(s)}
                  className="focus-visible:ring-ring flex min-h-8 items-center gap-1.5 rounded-l-md py-1 pl-2 outline-none focus-visible:ring-2"
                >
                  <SurfaceIcon s={s} className="size-3.5 shrink-0" />
                  <span className="max-w-32 truncate">{surfaceLabel(s)}</span>
                  {s.kind === "agents" && agentCount > 0 && (
                    <span className="bg-primary/15 text-primary rounded-full px-1.5 text-[10px] tabular-nums">
                      {agentCount}
                    </span>
                  )}
                </button>
                <button
                  type="button"
                  aria-label={`Close ${surfaceLabel(s)}`}
                  onClick={() => setPanel((p) => closeSurface(p, s.id))}
                  className="text-muted-foreground hover:text-foreground focus-visible:ring-ring flex min-h-8 items-center rounded-r-md py-1 pr-1.5 pl-1 opacity-60 outline-none focus-visible:ring-2 group-hover:opacity-100"
                >
                  <XIcon className="size-3" />
                </button>
              </span>
            );
          })}

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
              <DropdownMenuItem onSelect={() => addSurface({ id: "agents", kind: "agents" })}>
                <BotIcon className="size-3.5" /> Subagents
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setPanel((p) => openSurface(p, newTerminalSurface(p)))}>
                <TerminalIcon className="size-3.5" /> Terminal
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>

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
        {active?.kind === "agents" && <AgentsSurface items={items} />}
        {/* Terminals stay mounted while inactive: unmounting one hangs up its
            shell, and a tab switch must not kill a running command. */}
        {panel.surfaces
          .filter((s) => s.kind === "terminal")
          .map((s) => (
            <div key={s.id} className={cn("h-full", s.id !== panel.active && "hidden")}>
              <TerminalSurface target={{ session: sessionId }} />
            </div>
          ))}
      </div>
    </div>
  );
}

/**
 * The right-hand panel: a tabbed surface — diff, files, subagents, terminal —
 * docked to the right on a desktop and a full-screen sheet on a phone, where a
 * squeezed side panel would leave neither the transcript nor the panel
 * readable.
 */
export function Panel(props: PanelProps) {
  const isDesktop = useIsDesktop();
  const [width, setWidth] = useState(() => {
    const stored = Number(localStorage.getItem(WIDTH_KEY));
    return Number.isFinite(stored) && stored >= MIN_WIDTH ? stored : DEFAULT_WIDTH;
  });
  const dragging = useRef(false);

  // Escape closes the docked panel. The sheet does this for itself.
  useEffect(() => {
    if (!props.open || !isDesktop) return;
    const onKey = (e: KeyboardEvent) => {
      // A dialog or sheet over the panel owns Escape first; closing both at
      // once would dismiss something the user was not looking at.
      if (e.key !== "Escape" || document.querySelector("[role=dialog]")) return;
      props.onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [props.open, props.onClose, isDesktop]);

  useEffect(() => {
    if (!dragging.current) localStorage.setItem(WIDTH_KEY, String(width));
  }, [width]);

  const startDrag = useCallback((e: ReactPointerEvent) => {
    e.preventDefault();
    dragging.current = true;
    const max = () => Math.max(MIN_WIDTH, window.innerWidth - 360);
    const onMove = (m: PointerEvent) =>
      setWidth(Math.min(max(), Math.max(MIN_WIDTH, window.innerWidth - m.clientX)));
    const onUp = () => {
      dragging.current = false;
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      setWidth((w) => {
        localStorage.setItem(WIDTH_KEY, String(w));
        return w;
      });
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  }, []);

  if (!isDesktop) {
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
          <SheetTitle className="sr-only">Session panel</SheetTitle>
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
      aria-label="Session panel"
    >
      {!props.expanded && (
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize the panel"
          onPointerDown={startDrag}
          className="hover:bg-primary/40 absolute inset-y-0 -left-1 w-2 cursor-col-resize"
        />
      )}
      <PanelBody {...props} />
    </aside>
  );
}
