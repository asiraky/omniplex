import {
  ChevronRightIcon,
  ChevronsDownUpIcon,
  ChevronsUpDownIcon,
  FileDiffIcon,
  FolderIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "~/components/ui/button";
import { cn } from "~/lib/utils";
import { buildTree, collectDirs, fileName, type TreeNode } from "~/lib/tree";
import type { ChangedFile, TurnDiff } from "~/protocol";

// A preview stands in for a list too long to show: one file per area of the
// tree, so the three shown say as much as three can about where the work went.
const PREVIEW_FILES = 3;
const PREVIEW_SCOPES = 4;

const STATUS_LABEL: Record<string, string> = {
  added: "A",
  modified: "M",
  deleted: "D",
  renamed: "R",
  copied: "C",
};

const STATUS_TONE: Record<string, string> = {
  added: "text-success",
  modified: "text-attention-foreground",
  deleted: "text-destructive",
  renamed: "text-muted-foreground",
  copied: "text-muted-foreground",
};

/**
 * Line counts, abbreviated past a thousand. The two columns are a fixed width
 * so that every row's counts line up down the list rather than wandering with
 * the length of the number beside them.
 */
export function DiffStat({
  additions,
  deletions,
  binary,
  align = true,
}: {
  additions: number;
  deletions: number;
  binary?: boolean;
  align?: boolean;
}) {
  if (binary) return <span className="text-muted-foreground font-mono text-[10px]">binary</span>;
  return (
    <span
      role="group"
      aria-label={`${additions} added, ${deletions} removed`}
      className={cn(
        "shrink-0 font-mono text-[10px] tabular-nums",
        align ? "inline-grid grid-cols-[4ch_4ch] gap-1 text-right" : "inline-flex gap-1",
      )}
    >
      <span aria-hidden className="text-success">
        +{compact(additions)}
      </span>
      <span aria-hidden className="text-destructive">
        −{compact(deletions)}
      </span>
    </span>
  );
}

// A four-figure line count says nothing a two-figure one does not; the column
// is narrow and the shape of the number is what gets read.
function compact(n: number): string {
  if (n < 1000) return String(n);
  if (n < 1_000_000) return `${trim(n / 1000)}k`;
  return `${trim(n / 1_000_000)}m`;
}

function trim(n: number): string {
  return n < 10 ? n.toFixed(1).replace(/\.0$/, "") : String(Math.round(n));
}

// ---- the preview, for a change set too big to list ----

interface Scope {
  label: string;
  count: number;
}

/** Which areas of the tree the work landed in: `web · 4 files · internal · 2 files`. */
function summariseScopes(files: ChangedFile[]): Scope[] {
  const counts = new Map<string, number>();
  for (const file of files) {
    const segments = file.path.split("/").filter(Boolean);
    const scope = segments.length > 1 ? segments[0] : "root";
    counts.set(scope, (counts.get(scope) ?? 0) + 1);
  }
  return [...counts.entries()]
    .map(([label, count]) => ({ label, count }))
    .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label))
    .slice(0, PREVIEW_SCOPES);
}

/**
 * A handful of files that stand for the rest. One per area first, so the
 * preview describes the spread of the work rather than three files that happen
 * to sit in the same folder.
 */
function previewFiles(files: ChangedFile[]): ChangedFile[] {
  const seen = new Set<string>();
  const picked: ChangedFile[] = [];

  for (const file of files) {
    const segments = file.path.split("/").filter(Boolean);
    const scope = segments.length > 1 ? segments[0] : "root";
    if (seen.has(scope)) continue;
    seen.add(scope);
    picked.push(file);
    if (picked.length === PREVIEW_FILES) return picked;
  }
  const taken = new Set(picked);
  for (const file of files) {
    if (taken.has(file)) continue;
    picked.push(file);
    if (picked.length === PREVIEW_FILES) break;
  }
  return picked;
}

// ---- rows ----

function FileRow({
  node,
  depth,
  indented,
  onOpen,
}: {
  node: TreeNode<ChangedFile>;
  depth: number;
  indented: boolean;
  onOpen: (path: string) => void;
}) {
  const file = node.file!;
  return (
    <button
      type="button"
      onClick={() => onOpen(file.path)}
      style={{ paddingLeft: 8 + depth * 14 }}
      title={file.path}
      className="hover:bg-accent/50 focus-visible:ring-ring group flex min-h-11 w-full items-center gap-2 rounded-md py-1 pr-2 text-left transition-colors outline-none focus-visible:ring-2 md:min-h-0"
    >
      {indented && <span className="size-3.5 shrink-0" aria-hidden />}
      <span
        className={cn("w-3 shrink-0 text-center font-mono text-[11px]", STATUS_TONE[file.status])}
        title={file.status}
      >
        {STATUS_LABEL[file.status] ?? "M"}
      </span>
      <span className="text-muted-foreground/90 group-hover:text-foreground min-w-0 flex-1 truncate font-mono text-[11px]">
        {node.name}
        {file.oldPath && (
          <span className="text-muted-foreground"> ← {fileName(file.oldPath)}</span>
        )}
      </span>
      <DiffStat additions={file.additions} deletions={file.deletions} binary={file.binary} />
    </button>
  );
}

function DirectoryRow({
  node,
  depth,
  openDirs,
  onToggle,
  onOpen,
}: {
  node: TreeNode<ChangedFile>;
  depth: number;
  openDirs: Set<string>;
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
        <span className="text-muted-foreground min-w-0 flex-1 truncate font-mono text-[11px]">
          {node.name}
        </span>
        <DiffStat additions={node.additions} deletions={node.deletions} />
      </button>
      {open &&
        node.children?.map((child) => (
          <Row
            key={child.path}
            node={child}
            depth={depth + 1}
            openDirs={openDirs}
            onToggle={onToggle}
            onOpen={onOpen}
          />
        ))}
    </>
  );
}

function Row({
  node,
  depth,
  openDirs,
  onToggle,
  onOpen,
}: {
  node: TreeNode<ChangedFile>;
  depth: number;
  openDirs: Set<string>;
  onToggle: (path: string) => void;
  onOpen: (path: string) => void;
}) {
  // A path that is both a file and a directory gets a row for each. Rendering
  // only one of them would report a file in the count that is nowhere in the
  // list.
  return (
    <>
      {node.file && <FileRow node={node} depth={depth} indented onOpen={onOpen} />}
      {node.children && (
        <DirectoryRow node={node} depth={depth} openDirs={openDirs} onToggle={onToggle} onOpen={onOpen} />
      )}
    </>
  );
}

// ---- the card ----

/**
 * What one turn changed, under the turn that changed it.
 *
 * The event log cannot answer this: a formatter or a codemod changes files
 * without going through a tool call anyone parses. So the server snapshots the
 * checkout on either side of the turn and asks Git, and this renders the
 * answer — as a file tree that opens the diff panel on the file you click.
 */
export function ChangedFiles({
  diff,
  latest,
  onOpenDiff,
}: {
  diff: TurnDiff;
  /** Only the newest turn's collapsed card shows the preview chips. */
  latest: boolean;
  onOpenDiff: (path?: string) => void;
}) {
  const files = diff.files;
  const tree = useMemo(
    () => buildTree(files.map((f) => ({ path: f.path, file: f, additions: f.additions, deletions: f.deletions }))),
    [files],
  );
  const scopes = useMemo(() => summariseScopes(files), [files]);
  const preview = useMemo(() => previewFiles(files), [files]);

  // Collapsed until asked: the card is a summary line first, a tree second.
  const [expanded, setExpanded] = useState(false);
  const [collapsedDirs, setCollapsedDirs] = useState<Set<string>>(new Set());

  const dirPaths = useMemo(() => collectDirs(tree), [tree]);
  const openDirs = useMemo(
    () => new Set(dirPaths.filter((p) => !collapsedDirs.has(p))),
    [dirPaths, collapsedDirs],
  );
  const allOpen = collapsedDirs.size === 0;

  if (diff.error) {
    return (
      <div className="text-muted-foreground mt-1 flex items-center gap-2 rounded-lg border border-dashed px-3 py-2 text-[12px]">
        <TriangleAlertIcon className="size-3.5 shrink-0" />
        <span className="min-w-0 flex-1">This turn's changes could not be measured.</span>
      </div>
    );
  }
  if (files.length === 0) return null;

  return (
    <div className="bg-card/50 mt-1 rounded-xl border p-1.5">
      <div className={cn("flex items-center gap-1", expanded && "mb-1")}>
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          aria-expanded={expanded}
          className="hover:bg-accent/40 focus-visible:ring-ring flex min-h-11 min-w-0 flex-1 items-center gap-2 rounded-lg px-2 py-1.5 text-left transition-colors outline-none focus-visible:ring-2 md:min-h-0"
        >
          <ChevronRightIcon
            className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", expanded && "rotate-90")}
          />
          <span className="text-[12px]">
            {files.length} file{files.length === 1 ? "" : "s"} changed
          </span>
          <DiffStat additions={diff.additions} deletions={diff.deletions} align={false} />
          <span className="text-muted-foreground min-w-0 flex-1 truncate text-[11px]">
            {expanded ? "" : scopes.map((s) => `${s.label} · ${s.count}`).join("  ")}
          </span>
        </button>

        {expanded && dirPaths.length > 0 && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={allOpen ? "Collapse all folders" : "Expand all folders"}
            onClick={() => setCollapsedDirs(allOpen ? new Set(dirPaths) : new Set())}
          >
            {allOpen ? <ChevronsDownUpIcon /> : <ChevronsUpDownIcon />}
          </Button>
        )}
        <Button variant="ghost" size="sm" className="shrink-0 gap-1.5" onClick={() => onOpenDiff(files[0]?.path)}>
          <FileDiffIcon className="size-3.5" />
          <span className="hidden text-[12px] sm:inline">Open diff</span>
        </Button>
      </div>

      {expanded ? (
        <div>
          {tree.map((node) => (
            <Row
              key={node.path}
              node={node}
              depth={0}
              openDirs={openDirs}
              onToggle={(path) =>
                setCollapsedDirs((current) => {
                  const next = new Set(current);
                  if (next.has(path)) next.delete(path);
                  else next.add(path);
                  return next;
                })
              }
              onOpen={onOpenDiff}
            />
          ))}
          {diff.truncated && (
            <p className="text-muted-foreground px-2 py-1 text-[11px] italic">
              Only the first files are listed; this turn changed more than the card will show.
            </p>
          )}
        </div>
      ) : (
        latest && (
          <div className="flex flex-wrap items-center gap-1 px-1 pb-1">
            {preview.map((file) => (
              <button
                key={file.path}
                type="button"
                title={file.path}
                onClick={() => onOpenDiff(file.path)}
                className="hover:bg-accent/60 focus-visible:ring-ring flex items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-[10px] transition-colors outline-none focus-visible:ring-2"
              >
                <span className={cn(STATUS_TONE[file.status])}>{STATUS_LABEL[file.status] ?? "M"}</span>
                <span className="max-w-40 truncate">{fileName(file.path)}</span>
                <DiffStat additions={file.additions} deletions={file.deletions} binary={file.binary} align={false} />
              </button>
            ))}
            {files.length > preview.length && (
              <button
                type="button"
                onClick={() => setExpanded(true)}
                className="text-muted-foreground hover:text-foreground rounded-md px-2 py-1 text-[11px] transition-colors"
              >
                Show all {files.length} files
              </button>
            )}
          </div>
        )
      )}
    </div>
  );
}
