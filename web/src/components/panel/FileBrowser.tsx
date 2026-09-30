import { ChevronRightIcon, EyeIcon, EyeOffIcon, FolderIcon, PanelRightCloseIcon, PanelRightOpenIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { IconButton } from "~/components/IconButton";
import { Spinner } from "~/components/ui/spinner";
import { fileIconFor } from "~/lib/fileIcons";
import { buildTree, type TreeNode } from "~/lib/tree";
import { cn } from "~/lib/utils";
import type { FileContent, FileTree } from "~/protocol";
import { useLatest } from "~/useLatest";

// ---- the worktree tree ----

function FileRow({
  node,
  depth,
  selected,
  changed,
  onOpen,
}: {
  node: TreeNode<string>;
  depth: number;
  selected: boolean;
  changed: boolean;
  onOpen: (path: string) => void;
}) {
  const { Icon, tone } = fileIconFor(node.path);
  return (
    <button
      type="button"
      onClick={() => onOpen(node.path)}
      title={node.path}
      style={{ paddingLeft: 8 + depth * 14 }}
      className={cn(
        "focus-visible:ring-ring group flex min-h-11 w-full items-center gap-2 rounded-md py-1 pr-2 text-left transition-colors outline-none focus-visible:ring-2 md:min-h-0",
        selected ? "bg-accent" : "hover:bg-accent/50",
      )}
    >
      <span className="size-3.5 shrink-0" aria-hidden />
      <Icon className={cn("size-3.5 shrink-0", tone || "text-muted-foreground/70")} />
      <span
        className={cn(
          "min-w-0 flex-1 truncate font-mono text-[11px]",
          selected ? "text-foreground" : "text-muted-foreground/90 group-hover:text-foreground",
        )}
      >
        {node.name}
      </span>
      {changed && <span className="bg-attention-foreground/70 size-1.5 shrink-0 rounded-full" title="Changed in this thread" />}
    </button>
  );
}

function DirectoryRow({
  node,
  depth,
  openDirs,
  selectedPath,
  changedPaths,
  onToggle,
  onOpen,
}: {
  node: TreeNode<string>;
  depth: number;
  openDirs: Set<string>;
  selectedPath?: string;
  changedPaths: Set<string>;
  onToggle: (path: string) => void;
  onOpen: (path: string) => void;
}) {
  const open = openDirs.has(node.path);
  return (
    <>
      <button
        type="button"
        onClick={() => onToggle(node.path)}
        aria-expanded={open}
        style={{ paddingLeft: 8 + depth * 14 }}
        className="hover:bg-accent/50 focus-visible:ring-ring flex min-h-11 w-full items-center gap-2 rounded-md py-1 pr-2 text-left transition-colors outline-none focus-visible:ring-2 md:min-h-0"
      >
        <ChevronRightIcon
          className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", open && "rotate-90")}
        />
        <FolderIcon className="text-muted-foreground/70 size-3.5 shrink-0" />
        <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-[11px]">{node.name}</span>
      </button>
      {open &&
        node.children?.map((child) =>
          child.children ? (
            <DirectoryRow
              key={child.path}
              node={child}
              depth={depth + 1}
              openDirs={openDirs}
              selectedPath={selectedPath}
              changedPaths={changedPaths}
              onToggle={onToggle}
              onOpen={onOpen}
            />
          ) : (
            <FileRow
              key={child.path}
              node={child}
              depth={depth + 1}
              selected={child.path === selectedPath}
              changed={changedPaths.has(child.path)}
              onOpen={onOpen}
            />
          ),
        )}
    </>
  );
}

// ---- the file content viewer ----

/**
 * A file's lines, each keyed by its text and how many times that text has come
 * before it: a stable identity that survives a re-read which shifts lines.
 */
function keyedLines(content: string): { key: string; text: string }[] {
  const lines = content.split("\n");
  // A trailing newline yields one phantom empty line nobody wrote.
  if (lines[lines.length - 1] === "") lines.pop();
  const seen = new Map<string, number>();
  return lines.map((text) => {
    const n = (seen.get(text) ?? 0) + 1;
    seen.set(text, n);
    return { key: `${n}:${text}`, text };
  });
}

/** Every directory above a path, outermost first. */
function ancestors(path: string | undefined): string[] {
  if (!path) return [];
  const segments = path.split("/");
  return segments.slice(1).map((_, i) => segments.slice(0, i + 1).join("/"));
}

function FileView({ path, loadFile, line }: { path: string; loadFile: (path: string) => Promise<FileContent>; line?: number }) {
  const [file, setFile] = useState<FileContent | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const lineRefs = useRef(new Map<number, HTMLTableRowElement>());
  const loadRef = useLatest(loadFile);

  useEffect(() => {
    let stale = false;
    setLoading(true);
    setError("");
    loadRef.current(path)
      .then((f) => {
        if (stale) return;
        setFile(f);
        setLoading(false);
      })
      .catch((e) => {
        if (stale) return;
        setError(e instanceof Error ? e.message : String(e));
        setLoading(false);
      });
    return () => {
      stale = true;
    };
  }, [path, loadRef]);

  // Scroll to the requested line once the content is on screen.
  useEffect(() => {
    if (!file || line === undefined) return;
    lineRefs.current.get(line)?.scrollIntoView({ block: "center" });
  }, [file, line]);

  if (loading) {
    return (
      <p className="text-muted-foreground flex items-center gap-2 px-3 py-4 text-[12px]">
        <Spinner className="text-primary size-3.5" /> Reading {path}…
      </p>
    );
  }
  if (error) {
    return (
      <div className="text-destructive flex items-start gap-2 px-3 py-3 text-[12px]">
        <TriangleAlertIcon className="size-4 shrink-0" />
        <span className="min-w-0 break-words">{error}</span>
      </div>
    );
  }
  if (!file) return null;
  if (file.binary) {
    return <p className="text-muted-foreground px-3 py-4 text-[12px]">Binary file — nothing to show as text.</p>;
  }

  const lines = keyedLines(file.content);

  return (
    <div className="scroll-thin h-full overflow-auto overscroll-contain">
      <table className="w-full border-collapse font-mono text-[11.5px] leading-relaxed">
        <tbody>
          {lines.map(({ key, text }, i) => (
            <tr
              key={key}
              ref={(el) => {
                if (el) lineRefs.current.set(i + 1, el);
                else lineRefs.current.delete(i + 1);
              }}
              className={cn(i + 1 === line && "bg-attention/40")}
            >
              <td className="text-muted-foreground/50 w-[1%] min-w-10 pr-3 pl-2 text-right align-top select-none">
                {i + 1}
              </td>
              <td className="pr-3 break-words whitespace-pre-wrap">{text}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {file.truncated && (
        <p className="text-muted-foreground px-3 py-2 text-[11px] italic">
          Truncated — the file is larger than the viewer will show.
        </p>
      )}
    </div>
  );
}

/**
 * The worktree browser: the thread's real file tree, read-only, with the
 * selected file's contents beside it. With nothing selected it is the files
 * tab; with a selection it is a `file:` tab, where the tree shrinks to a side
 * rail and can be hidden entirely.
 */
export function FileBrowser({
  tree,
  loading,
  error,
  onRefresh,
  includeIgnored,
  onToggleIgnored,
  changedPaths,
  selectedPath,
  line,
  onSelect,
  loadFile,
}: {
  tree: FileTree | null;
  loading: boolean;
  error: string;
  onRefresh: () => void;
  includeIgnored: boolean;
  onToggleIgnored: () => void;
  changedPaths: Set<string>;
  selectedPath?: string;
  line?: number;
  onSelect: (path: string) => void;
  loadFile: (path: string) => Promise<FileContent>;
}) {
  const [treeHidden, setTreeHidden] = useState(false);
  const nodes = useMemo(
    () => buildTree((tree?.files ?? []).map((p) => ({ path: p, file: p }))),
    [tree],
  );
  // Directories start closed; a worktree is big and the top level is the map.
  // A selected file's ancestors open themselves, so the tree shows where the
  // file lives rather than a closed top level. That happens during render
  // when the selection changes, so the tree never paints closed first.
  const [openDirs, setOpenDirs] = useState<Set<string>>(() => new Set(ancestors(selectedPath)));
  const [openedFor, setOpenedFor] = useState(selectedPath);
  if (openedFor !== selectedPath) {
    setOpenedFor(selectedPath);
    if (selectedPath) setOpenDirs((current) => new Set([...current, ...ancestors(selectedPath)]));
  }

  const toggle = (path: string) =>
    setOpenDirs((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });

  const treePane = (
    <div className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain p-1">
      {error && (
        <div className="text-destructive flex items-start gap-2 px-2 py-2 text-[12px]">
          <TriangleAlertIcon className="size-4 shrink-0" />
          <span className="min-w-0 break-words">{error}</span>
        </div>
      )}
      {!error && tree?.warning && (
        <p className="text-muted-foreground px-2 py-6 text-center text-[12px]">{tree.warning}</p>
      )}
      {!error && !tree?.warning && nodes.length === 0 && (
        <p className="text-muted-foreground px-2 py-10 text-center text-[12px]">
          {loading ? "Reading the worktree…" : "The worktree is empty."}
        </p>
      )}
      {nodes.map((node) =>
        node.children ? (
          <DirectoryRow
            key={node.path}
            node={node}
            depth={0}
            openDirs={openDirs}
            selectedPath={selectedPath}
            changedPaths={changedPaths}
            onToggle={toggle}
            onOpen={onSelect}
          />
        ) : (
          <FileRow
            key={node.path}
            node={node}
            depth={0}
            selected={node.path === selectedPath}
            changed={changedPaths.has(node.path)}
            onOpen={onSelect}
          />
        ),
      )}
      {tree?.truncated && (
        <p className="text-muted-foreground px-2 py-2 text-[11px] italic">
          Only the first files are listed; the worktree holds more than the tree will show.
        </p>
      )}
    </div>
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-1 border-b px-2 py-1">
        <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-[10px]" title={selectedPath ?? tree?.root}>
          {selectedPath ?? tree?.root ?? "…"}
        </span>
        {selectedPath && (
          <IconButton
            label={treeHidden ? "Show the tree" : "Hide the tree"}
            onClick={() => setTreeHidden((v) => !v)}
          >
            {treeHidden ? <PanelRightOpenIcon /> : <PanelRightCloseIcon />}
          </IconButton>
        )}
        <IconButton
          label={includeIgnored ? "Hide gitignored files" : "Show gitignored files"}
          onClick={onToggleIgnored}
        >
          {includeIgnored ? <EyeIcon /> : <EyeOffIcon />}
        </IconButton>
        <IconButton label="Re-read the worktree" onClick={onRefresh}>
          <RefreshCwIcon className={cn(loading && "animate-spin")} />
        </IconButton>
      </div>

      {selectedPath ? (
        // Content beside the tree — content first, tree as a right-hand rail,
        // matching where the tab strip and controls already live.
        <div className="flex min-h-0 flex-1">
          <div className="min-w-0 flex-1">
            <FileView path={selectedPath} loadFile={loadFile} line={line} />
          </div>
          {!treeHidden && (
            <div className="flex w-[min(16rem,44%)] shrink-0 flex-col border-l">{treePane}</div>
          )}
        </div>
      ) : (
        treePane
      )}
    </div>
  );
}
