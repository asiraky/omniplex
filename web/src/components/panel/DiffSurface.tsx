import { ChevronRightIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Diff } from "~/components/Diff";
import { IconButton } from "~/components/IconButton";
import { Checkbox } from "~/components/ui/checkbox";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "~/components/ui/select";
import { Spinner } from "~/components/ui/spinner";
import { useDiffWrap } from "~/lib/diffWrap";
import { cn } from "~/lib/utils";
import type { ChangedFile, DiffComparison, FileDiff, PullRequest, ThreadChanges } from "~/protocol";
import { useLatest } from "~/useLatest";

type DiffEntry = { diff?: FileDiff; loading: boolean; error?: string };
type DiffsRead = { changes: ThreadChanges | null; diffs: Record<string, DiffEntry> };
const NO_DIFFS: Record<string, DiffEntry> = {};

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

function Counts({ additions, deletions, binary }: { additions: number; deletions: number; binary?: boolean }) {
  if (binary) return <span className="text-muted-foreground font-mono text-[10px]">binary</span>;
  return (
    <span className="shrink-0 font-mono text-[10px] tabular-nums">
      <span className="text-success">+{additions}</span>{" "}
      <span className="text-destructive">−{deletions}</span>
    </span>
  );
}

function FileRow({
  file,
  rowRef,
  expanded,
  diff,
  loading,
  error,
  wrap,
  onToggle,
}: {
  file: ChangedFile;
  rowRef?: (el: HTMLDivElement | null) => void;
  expanded: boolean;
  diff?: FileDiff;
  loading: boolean;
  error?: string;
  wrap: boolean;
  onToggle: () => void;
}) {
  const slash = file.path.lastIndexOf("/");
  const dir = slash === -1 ? "" : file.path.slice(0, slash + 1);
  const name = slash === -1 ? file.path : file.path.slice(slash + 1);

  return (
    <div ref={rowRef} className="border-b last:border-b-0">
      <button
        type="button"
        onClick={onToggle}
        aria-expanded={expanded}
        className="hover:bg-accent/40 focus-visible:ring-ring flex min-h-11 w-full items-center gap-2 px-2 py-1.5 text-left outline-none focus-visible:ring-2 md:min-h-0"
      >
        <ChevronRightIcon
          className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", expanded && "rotate-90")}
        />
        <span
          className={cn("w-3 shrink-0 text-center font-mono text-[11px]", STATUS_TONE[file.status])}
          title={file.status}
        >
          {STATUS_LABEL[file.status] ?? "M"}
        </span>
        <span className="min-w-0 flex-1 truncate font-mono text-[12px]" title={file.path}>
          <span className="text-muted-foreground">{dir}</span>
          {name}
          {file.oldPath && (
            <span className="text-muted-foreground"> ← {file.oldPath}</span>
          )}
        </span>
        <Counts additions={file.additions} deletions={file.deletions} binary={file.binary} />
      </button>

      {expanded && (
        <div className="bg-muted/20 border-t">
          {loading && (
            <p className="text-muted-foreground flex items-center gap-2 px-3 py-2 text-[12px]">
              <Spinner className="text-primary size-3.5" /> Reading the diff…
            </p>
          )}
          {error && <p className="text-destructive px-3 py-2 font-mono text-[11px]">{error}</p>}
          {diff && !loading && !error && (
            <div
              className={cn(
                "scroll-thin max-h-[60vh] overscroll-contain",
                wrap ? "overflow-y-auto" : "overflow-auto",
              )}
            >
              {diff.binary ? (
                <p className="text-muted-foreground px-3 py-2 text-[12px]">
                  Binary file — nothing to show as text.
                </p>
              ) : (
                <>
                  <Diff patch={diff.patch} wrap={wrap} />
                  {diff.truncated && (
                    <p className="text-muted-foreground px-3 py-2 text-[11px] italic">
                      Diff truncated — open the file in the worktree to see the rest.
                    </p>
                  )}
                </>
              )}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * The changed-file list with inline unified diffs — what the whole panel used
 * to be, now one surface among several. The change list itself is owned by the
 * panel (other surfaces route on it); the per-file diffs are read here,
 * lazily, and dropped whenever the list is re-read.
 */
export function DiffSurface({
  changes,
  loading,
  error,
  onRefresh,
  loadDiff,
  reveal,
  comparison,
  onComparisonChange,
  pr,
}: {
  changes: ThreadChanges | null;
  loading: boolean;
  error: string;
  onRefresh: () => void;
  loadDiff: (path: string, changes: ThreadChanges) => Promise<FileDiff>;
  reveal?: { path: string; nonce: number } | null;
  comparison: DiffComparison;
  onComparisonChange: (comparison: DiffComparison) => void;
  pr?: PullRequest | null;
}) {
  const [expanded, setExpanded] = useState<string | null>(null);
  const [wrap, setWrap] = useDiffWrap();
  // A new change list describes a worktree that has moved on; every diff read
  // against the old one is stale. So the diffs are held with the list they
  // were read against, and a list that is no longer the current one reads as
  // no diffs at all. An in-flight read from before a refresh lands on the old
  // list and is never seen.
  const [read, setRead] = useState<DiffsRead>({ changes, diffs: {} });
  const diffs = read.changes === changes ? read.diffs : NO_DIFFS;

  // Row elements, so a revealed file can be scrolled to.
  const rowRefs = useRef(new Map<string, HTMLDivElement>());
  const diffRef = useLatest(loadDiff);

  const toggle = useCallback((path: string, forceOpen = false) => {
    setExpanded((current) => (current === path && !forceOpen ? null : path));
    // Expanding a row twice does not re-read it; a failed read may be retried.
    const known = diffs[path];
    if (known && !known.error) return;
    // The first entry for a new list starts it afresh.
    setRead((r) => ({
      changes,
      diffs: { ...(r.changes === changes ? r.diffs : NO_DIFFS), [path]: { loading: true } },
    }));
    if (!changes) return;
    // An answer for a list that has since been replaced is dropped.
    const land = (entry: DiffEntry) =>
      setRead((r) => (r.changes === changes ? { changes, diffs: { ...r.diffs, [path]: entry } } : r));
    void diffRef.current(path, changes)
      .then((diff) => land({ diff, loading: false }))
      .catch((e) => land({ loading: false, error: e instanceof Error ? e.message : String(e) }));
  }, [changes, diffs, diffRef]);

  const toggleRef = useLatest(toggle);

  const files = changes?.files ?? [];

  // Reveal whatever the transcript asked for, once the list it belongs to has
  // arrived. A path the list does not carry is not an error: the file may have
  // been changed by an earlier turn and put back since.
  useEffect(() => {
    if (!reveal) return;
    if (!files.some((f) => f.path === reveal.path)) return;
    toggleRef.current(reveal.path, true);
    const row = rowRefs.current.get(reveal.path);
    row?.scrollIntoView({ block: "start", behavior: "smooth" });
    // The nonce is what makes a repeat click count as a new request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reveal?.path, reveal?.nonce, files]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5">
        <Select value={comparison} onValueChange={(value) => onComparisonChange(value as DiffComparison)}>
          <SelectTrigger aria-label="Diff comparison" className="h-8 min-w-0 max-w-52 text-[12px]">
            <SelectValue>
              {comparison === "uncommitted" && (
                <>
                  <span className="sm:hidden">Uncommitted</span>
                  <span className="hidden sm:inline">Uncommitted changes</span>
                </>
              )}
              {comparison === "branch" && "Branch changes"}
              {comparison === "pull_request" && (pr ? `PR #${pr.number}` : "Attached PR")}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="uncommitted">Uncommitted changes</SelectItem>
            <SelectItem value="branch">Branch changes</SelectItem>
            {(pr || comparison === "pull_request") && <SelectItem value="pull_request">{pr ? `PR #${pr.number} vs ${pr.baseRefName || "base"}` : "Attached PR"}</SelectItem>}
          </SelectContent>
        </Select>
        <span className="text-muted-foreground shrink-0 text-[11px] whitespace-nowrap">
          {files.length} file{files.length === 1 ? "" : "s"}
        </span>
        <span className="flex-1" />
        {changes && <Counts additions={changes.additions} deletions={changes.deletions} />}
        <label className="flex min-h-11 shrink-0 cursor-pointer items-center gap-1.5 text-[11px] select-none md:min-h-0">
          <Checkbox checked={wrap} onCheckedChange={(v) => setWrap(v === true)} className="size-3.5" />
          Wrap text
        </label>
        <IconButton label="Re-read the worktree" onClick={onRefresh}>
          <RefreshCwIcon className={cn(loading && "animate-spin")} />
        </IconButton>
      </div>

      <div className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain">
        {error && (
          <div className="text-destructive flex items-start gap-2 px-3 py-3 text-[12px]">
            <TriangleAlertIcon className="size-4 shrink-0" />
            <span className="min-w-0 break-words">{error}</span>
          </div>
        )}
        {!error && changes?.warning && (
          <p className="text-muted-foreground px-3 py-6 text-center text-[12px]">{changes.warning}</p>
        )}
        {!error && !changes?.warning && files.length === 0 && (
          <p className="text-muted-foreground px-3 py-10 text-center text-[12px]">
            {loading && !changes ? "Reading the worktree…" : "Nothing changed yet."}
          </p>
        )}
        {files.map((f) => (
          <FileRow
            key={f.path}
            file={f}
            rowRef={(el) => {
              if (el) rowRefs.current.set(f.path, el);
              else rowRefs.current.delete(f.path);
            }}
            expanded={expanded === f.path}
            diff={diffs[f.path]?.diff}
            loading={!!diffs[f.path]?.loading}
            error={diffs[f.path]?.error}
            wrap={wrap}
            onToggle={() => toggle(f.path)}
          />
        ))}
        {changes?.truncated && (
          <p className="text-muted-foreground px-3 py-2 text-[11px] italic">
            Only the first files are listed; this thread changed more than the panel will show.
          </p>
        )}
      </div>
    </div>
  );
}
