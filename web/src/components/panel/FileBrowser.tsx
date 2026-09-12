import { AtSignIcon, ChevronRightIcon, EyeIcon, EyeOffIcon, FolderIcon, PanelRightCloseIcon, PanelRightOpenIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { CodeLines, selectedLineRange } from "~/components/CodeLines";
import { IconButton } from "~/components/IconButton";
import { Spinner } from "~/components/ui/spinner";
import { setRefDrag, type FileRef } from "~/lib/composerRefs";
import { watchCopies } from "~/lib/copyOrigin";
import { fileIconFor } from "~/lib/fileIcons";
import { buildTree, type TreeNode } from "~/lib/tree";
import { cn } from "~/lib/utils";
import type { FileContent, FileTree } from "~/protocol";

// ---- the worktree tree ----

function FileRow({
  node,
  depth,
  selected,
  changed,
  onOpen,
  onMention,
}: {
  node: TreeNode<string>;
  depth: number;
  selected: boolean;
  changed: boolean;
  onOpen: (path: string) => void;
  onMention?: (path: string) => void;
}) {
  const { Icon, tone } = fileIconFor(node.path);
  return (
    <div
      className={cn(
        "group relative flex items-center rounded-md transition-colors",
        selected ? "bg-accent" : "hover:bg-accent/50",
      )}
    >
      <button
        type="button"
        onClick={() => onOpen(node.path)}
        title={node.path}
        // The row is the drag handle. `text/plain` on the drag is the token
        // itself, which is what lets a browser that will not report a caret
        // position still drop the mention in the right place.
        draggable
        onDragStart={(e) => setRefDrag(e.dataTransfer, { path: node.path })}
        style={{ paddingLeft: 8 + depth * 14 }}
        className="focus-visible:ring-ring flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-md py-1 pr-1 text-left outline-none focus-visible:ring-2 md:min-h-0"
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
        {changed && (
          <span
            className="bg-attention-foreground/70 size-1.5 shrink-0 rounded-full"
            title="Changed in this session"
          />
        )}
      </button>
      {/* Dragging a row onto the composer is a desktop gesture — on a phone the
          panel *is* the screen, so there is nowhere to drag to. This button is
          the same action for a thumb, and it stays visible there rather than
          waiting for a hover that never comes. */}
      {onMention && (
        <button
          type="button"
          onClick={() => onMention(node.path)}
          aria-label={`Mention ${node.path} in the message`}
          title="Add to the message"
          className="text-muted-foreground hover:text-foreground hover:bg-accent focus-visible:ring-ring mr-1 grid size-8 shrink-0 place-items-center rounded-md outline-none focus-visible:ring-2 focus-visible:opacity-100 md:size-6 md:opacity-0 md:group-hover:opacity-100"
        >
          <AtSignIcon className="size-3.5" />
        </button>
      )}
    </div>
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
  onMention,
}: {
  node: TreeNode<string>;
  depth: number;
  openDirs: Set<string>;
  selectedPath?: string;
  changedPaths: Set<string>;
  onToggle: (path: string) => void;
  onOpen: (path: string) => void;
  onMention?: (path: string) => void;
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
              onMention={onMention}
            />
          ) : (
            <FileRow
              key={child.path}
              node={child}
              depth={depth + 1}
              selected={child.path === selectedPath}
              changed={changedPaths.has(child.path)}
              onOpen={onOpen}
              onMention={onMention}
            />
          ),
        )}
    </>
  );
}

// ---- the file content viewer ----

function FileView({ path, loadFile, line }: { path: string; loadFile: (path: string) => Promise<FileContent>; line?: number }) {
  const [file, setFile] = useState<FileContent | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const hostRef = useRef<HTMLDivElement>(null);
  const loadRef = useRef(loadFile);
  loadRef.current = loadFile;

  // Copying a chunk of a file records which chunk it was, so pasting it into
  // the composer produces a chip that says "App.tsx:40-91" instead of an
  // anonymous lump of text. Best effort: a selection that started outside the
  // table has no line to report and is left as an ordinary paste.
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    return watchCopies(host, () => {
      const range = selectedLineRange(window.getSelection());
      return range ? { kind: "file", path, from: range.from, to: range.to } : null;
    });
    // Re-run once the content lands: the host does not exist while the file is
    // still being read, so binding on mount alone would bind to nothing.
  }, [path, file]);

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
  }, [path]);

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

  return (
    <div ref={hostRef} className="scroll-thin h-full overflow-auto overscroll-contain">
      <CodeLines content={file.content} highlight={line} />
      {file.truncated && (
        <p className="text-muted-foreground px-3 py-2 text-[11px] italic">
          Truncated — the file is larger than the viewer will show.
        </p>
      )}
    </div>
  );
}

/**
 * The worktree browser: the session's real file tree, read-only, with the
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
  onMention,
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
  /** Writes a `@path` chip into the composer. Absent when there is no composer
      to write into, which is when the row's button is not offered at all. */
  onMention?: (ref: FileRef) => void;
}) {
  const [treeHidden, setTreeHidden] = useState(false);
  const mentionPath = useCallback((path: string) => onMention?.({ path }), [onMention]);
  const nodes = useMemo(
    () => buildTree((tree?.files ?? []).map((p) => ({ path: p, file: p }))),
    [tree],
  );
  // Directories start closed; a worktree is big and the top level is the map.
  const [openDirs, setOpenDirs] = useState<Set<string>>(new Set());

  // A selected file's ancestors open themselves, so the tree shows where the
  // file lives rather than a closed top level.
  useEffect(() => {
    if (!selectedPath) return;
    setOpenDirs((current) => {
      const next = new Set(current);
      const segments = selectedPath.split("/");
      for (let i = 1; i < segments.length; i++) next.add(segments.slice(0, i).join("/"));
      return next;
    });
  }, [selectedPath]);

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
            onMention={onMention && mentionPath}
          />
        ) : (
          <FileRow
            key={node.path}
            node={node}
            depth={0}
            selected={node.path === selectedPath}
            changed={changedPaths.has(node.path)}
            onOpen={onSelect}
            onMention={onMention && mentionPath}
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
        {selectedPath && onMention && (
          <IconButton label="Add this file to the message" onClick={() => onMention({ path: selectedPath })}>
            <AtSignIcon />
          </IconButton>
        )}
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
