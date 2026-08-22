import { ChevronRightIcon, RefreshCwIcon, TriangleAlertIcon } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

import { Diff } from "~/components/Diff";
import { IconButton } from "~/components/IconButton";
import { Checkbox } from "~/components/ui/checkbox";
import { Spinner } from "~/components/ui/spinner";
import { useDiffWrap } from "~/lib/diffWrap";
import { cn } from "~/lib/utils";
import type { ChangedFile, FileDiff, SessionChanges } from "~/protocol";

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
}: {
  changes: SessionChanges | null;
  loading: boolean;
  error: string;
  onRefresh: () => void;
  loadDiff: (path: string) => Promise<FileDiff>;
  reveal?: { path: string; nonce: number } | null;
}) {
  const [expanded, setExpanded] = useState<string | null>(null);
  const [wrap, setWrap] = useDiffWrap();
  const [diffs, setDiffs] = useState<Record<string, { diff?: FileDiff; loading: boolean; error?: string }>>({});

  // Paths already asked for, so expanding a row twice does not re-read it.
  const requested = useRef(new Set<string>());
  // Row elements, so a revealed file can be scrolled to.
  const rowRefs = useRef(new Map<string, HTMLDivElement>());
  const diffRef = useRef(loadDiff);
  diffRef.current = loadDiff;

  // A new change list describes a worktree that has moved on; every diff read
  // against the old one is stale. The generation is what keeps an in-flight
  // read from before the refresh from overwriting one made after it — merely
  // checking `requested` would accept the stale answer the moment the same
  // path was asked for again.
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    requested.current.clear();
    setDiffs({});
  }, [changes]);

  const toggle = useCallback((path: string, forceOpen = false) => {
    setExpanded((current) => (current === path && !forceOpen ? null : path));
    if (requested.current.has(path)) return;
    requested.current.add(path);
    const mine = generation.current;
    setDiffs((d) => ({ ...d, [path]: { loading: true } }));
    void diffRef.current(path)
      .then((diff) => {
        if (mine !== generation.current) return;
        setDiffs((d) => ({ ...d, [path]: { diff, loading: false } }));
      })
      .catch((e) => {
        if (mine !== generation.current) return;
        requested.current.delete(path);
        setDiffs((d) => ({
          ...d,
          [path]: { loading: false, error: e instanceof Error ? e.message : String(e) },
        }));
      });
  }, []);

  const toggleRef = useRef(toggle);
  toggleRef.current = toggle;

  const files = changes?.files ?? [];

  // Reveal whatever the transcript asked for, once the list it belongs to has
  // arrived. A path the list does not carry is not an error: the file may have
  // been changed by an earlier turn and put back since.
  useEffect(() => {
    if (!reveal) return;
    if (!files.some((f) => f.path === reveal.path)) return;
    setExpanded(reveal.path);
    if (!requested.current.has(reveal.path)) toggleRef.current(reveal.path, true);
    const row = rowRefs.current.get(reveal.path);
    row?.scrollIntoView({ block: "start", behavior: "smooth" });
    // The nonce is what makes a repeat click count as a new request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reveal?.path, reveal?.nonce, files]);

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5">
        <span className="shrink-0 text-[12px] whitespace-nowrap">
          {files.length} file{files.length === 1 ? "" : "s"} changed
        </span>
        <span className="text-muted-foreground min-w-0 truncate font-mono text-[10px]">
          {changes?.branch ?? "…"}
          {changes?.baseRef && ` vs ${changes.baseRef}`}
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
            Only the first files are listed; this session changed more than the panel will show.
          </p>
        )}
      </div>
    </div>
  );
}
