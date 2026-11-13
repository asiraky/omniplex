import {
  ChevronRightIcon,
  Columns2Icon,
  FlaskConicalOffIcon,
  PilcrowIcon,
  RefreshCwIcon,
  TriangleAlertIcon,
  UnfoldVerticalIcon,
  WrapTextIcon,
} from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";

import { Diff } from "~/components/Diff";
import { IconButton } from "~/components/IconButton";
import { DiffScopeSelect, type CommitRange } from "~/components/panel/DiffScopeSelect";
import { DiffFindBar, DiffToolbar, type DiffToggle } from "~/components/panel/DiffToolbar";
import { diffPlace, diffPlaceKey, rememberDiffPlace } from "~/components/panel/diffScroll";
import { Spinner } from "~/components/ui/spinner";
import {
  useDiffFullContext,
  useDiffHideTests,
  useDiffIgnoreWhitespace,
  useDiffSplit,
  useDiffWrap,
} from "~/lib/diffPrefs";
import type { DiffLine } from "~/lib/patch";
import { reviewComment } from "~/lib/reviewComment";
import { isTestPath } from "~/lib/testPath";
import { toast } from "~/lib/toast";
import { cn } from "~/lib/utils";
import type {
  BranchCommits,
  ChangedFile,
  DiffComparison,
  DiffTurn,
  FileDiff,
  PullRequest,
  ThreadChanges,
} from "~/protocol";
import { useIsDesktop } from "~/useMediaQuery";
import { useLatest } from "~/useLatest";

export type { CommitRange };

/** How a file's diff is asked for. Each changes what the server sends back. */
export interface DiffOptions {
  ignoreWhitespace: boolean;
  fullContext: boolean;
}

type DiffEntry = { diff?: FileDiff; loading: boolean; error?: string };
/** Diffs, with the list and the options they were read under. */
type DiffsRead = { changes: ThreadChanges | null; options: string; diffs: Record<string, DiffEntry> };
const NO_DIFFS: Record<string, DiffEntry> = {};

// Long enough to mean something; a single letter matches every file.
const MIN_QUERY = 2;
// Typing a word costs one search, not one per key, on a connection where
// each is a second's round trip.
const FIND_DELAY = 300;

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
  layout,
  highlight,
  onToggle,
  onAddComment,
}: {
  file: ChangedFile;
  rowRef?: (el: HTMLDivElement | null) => void;
  expanded: boolean;
  diff?: FileDiff;
  loading: boolean;
  error?: string;
  wrap: boolean;
  layout: "unified" | "split";
  highlight?: string;
  onToggle: () => void;
  onAddComment?: (block: string) => void;
}) {
  const slash = file.path.lastIndexOf("/");
  const dir = slash === -1 ? "" : file.path.slice(0, slash + 1);
  const name = slash === -1 ? file.path : file.path.slice(slash + 1);

  // An old-side number is a line of the file under its old name, so a renamed
  // file's removed lines cite that name.
  const commentLine = onAddComment
    ? ({ line, side }: { line: DiffLine; side: "old" | "new" }, comment: string) => {
        const path = side === "old" ? (file.oldPath ?? file.path) : file.path;
        onAddComment(reviewComment(path, line, side, comment));
        toast.success("Added to the composer");
      }
    : undefined;

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
                  <Diff
                    patch={diff.patch}
                    wrap={wrap}
                    layout={layout}
                    highlight={highlight}
                    onCommentLine={commentLine}
                  />
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

/** Which list a remembered place belongs to. */
function scopeKey(comparison: DiffComparison, turn?: DiffTurn | null, commits?: CommitRange | null): string {
  if (comparison === "turn") return `turn:${turn?.id ?? ""}`;
  if (comparison === "commit") return `commit:${commits?.from ?? ""}..${commits?.to ?? ""}`;
  return comparison;
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/**
 * The changed-file list with inline diffs — what the whole panel used to be,
 * now one surface among several. The change list itself is owned by the panel
 * (other surfaces route on it); the per-file diffs are read here, lazily, and
 * dropped whenever the list is re-read or the way they are read changes.
 */
export function DiffSurface({
  threadId,
  changes,
  loading,
  error,
  onRefresh,
  loadDiff,
  loadCommits,
  findInDiff,
  reveal,
  onRevealHandled,
  comparison,
  turn,
  commits,
  onComparisonChange,
  pr,
  onAddComment,
}: {
  threadId: string;
  changes: ThreadChanges | null;
  loading: boolean;
  error: string;
  onRefresh: () => void;
  loadDiff: (path: string, changes: ThreadChanges, options: DiffOptions) => Promise<FileDiff>;
  loadCommits: () => Promise<BranchCommits>;
  /** The paths in a list whose changed lines contain a query. Path names are matched here. */
  findInDiff: (query: string, changes: ThreadChanges) => Promise<string[]>;
  /** A file to put on screen. Unhandled until `onRevealHandled` hears its nonce. */
  reveal?: { path: string; nonce: number } | null;
  onRevealHandled?: (nonce: number) => void;
  comparison: DiffComparison;
  /** The turn a card last opened, offered in the select so it can be gone back to. */
  turn?: DiffTurn | null;
  /** The commits a `commit` comparison covers. */
  commits?: CommitRange | null;
  onComparisonChange: (comparison: DiffComparison, commits?: CommitRange) => void;
  pr?: PullRequest | null;
  /** A comment on a diff line, as a block for the composer. Unset, lines cannot be commented on. */
  onAddComment?: (block: string) => void;
}) {
  const desktop = useIsDesktop();
  const [wrap, setWrap] = useDiffWrap();
  const [split, setSplit] = useDiffSplit();
  const [ignoreWhitespace, setIgnoreWhitespace] = useDiffIgnoreWhitespace();
  const [hideTests, setHideTests] = useDiffHideTests();
  const [fullContext, setFullContext] = useDiffFullContext();
  // A phone has no room for two columns of code, whatever was ticked on a desktop.
  const layout = desktop && split ? "split" : "unified";

  // ---- where this list was left ----
  // Remembered per thread and scope, so coming back (a tab switch, a closed
  // panel, another thread and back) opens the same file at the same scroll.
  // A reveal, once its file is listed, takes over from both.
  const placeKey = diffPlaceKey(threadId, scopeKey(comparison, turn, commits));
  const [shownKey, setShownKey] = useState(placeKey);
  const [expanded, setExpanded] = useState<string | null>(() => diffPlace(placeKey).expanded);
  // A test file a reveal asked for stays listed while tests are hidden.
  const [revealed, setRevealed] = useState<string | null>(() => diffPlace(placeKey).revealed);
  // The scrollTop still to be put back, once there is enough list to hold it.
  const [restore, setRestore] = useState<number | null>(() => diffPlace(placeKey).scrollTop || null);
  if (shownKey !== placeKey) {
    // Switching scope in place is coming back to that scope too.
    const place = diffPlace(placeKey);
    setShownKey(placeKey);
    setExpanded(place.expanded);
    setRevealed(place.revealed);
    setRestore(place.scrollTop || null);
  }
  useEffect(() => rememberDiffPlace(placeKey, { expanded, revealed }), [placeKey, expanded, revealed]);

  // ---- the diffs ----
  // A new change list describes a worktree that has moved on, and new options
  // ask for different text; every diff read under the old ones is stale. So
  // the diffs are held with the list and options they were read under, and
  // anything else reads as no diffs at all. An in-flight read from before
  // lands on the old key and is never seen.
  const options = `${ignoreWhitespace ? "w" : ""}${fullContext ? "c" : ""}`;
  const [read, setRead] = useState<DiffsRead>({ changes, options, diffs: {} });
  const diffs = read.changes === changes && read.options === options ? read.diffs : NO_DIFFS;
  const diffRef = useLatest(loadDiff);
  // What has been asked for under the current key, so a replayed effect
  // (StrictMode) does not ask twice.
  const asked = useRef<{ changes: ThreadChanges; options: string; paths: Set<string> } | null>(null);

  const readDiff = useCallback(
    (path: string) => {
      if (!changes) return;
      const a = asked.current;
      if (a && a.changes === changes && a.options === options) {
        if (a.paths.has(path)) return;
        a.paths.add(path);
      } else {
        asked.current = { changes, options, paths: new Set([path]) };
      }
      const same = (r: DiffsRead) => r.changes === changes && r.options === options;
      // The first entry for a new key starts it afresh.
      setRead((r) => ({ changes, options, diffs: { ...(same(r) ? r.diffs : NO_DIFFS), [path]: { loading: true } } }));
      const land = (entry: DiffEntry) => setRead((r) => (same(r) ? { ...r, diffs: { ...r.diffs, [path]: entry } } : r));
      void diffRef.current(path, changes, { ignoreWhitespace, fullContext })
        .then((diff) => land({ diff, loading: false }))
        .catch((e) => land({ loading: false, error: e instanceof Error ? e.message : String(e) }));
    },
    [changes, options, ignoreWhitespace, fullContext, diffRef],
  );

  const files = useMemo(() => changes?.files ?? [], [changes]);

  // ---- hide tests ----
  const testCount = useMemo(() => files.filter((f) => isTestPath(f.path)).length, [files]);
  const listed = useMemo(
    () => (hideTests && testCount > 0 ? files.filter((f) => !isTestPath(f.path) || f.path === revealed) : files),
    [files, hideTests, testCount, revealed],
  );
  const hiddenCount = files.length - listed.length;
  // The header's totals are for what is listed, so hidden files come off them.
  const totals = useMemo(() => {
    if (!changes) return null;
    let { additions, deletions } = changes;
    if (listed.length < files.length) {
      const shown = new Set(listed);
      for (const f of files) {
        if (shown.has(f)) continue;
        additions -= f.additions;
        deletions -= f.deletions;
      }
    }
    return { additions, deletions };
  }, [changes, files, listed]);

  // The open file always has its diff: read on expanding, and read again
  // against a new list or new options without being closed and reopened. A
  // file hidden from the list is not read, open or not.
  useEffect(() => {
    if (!expanded || diffs[expanded] || !listed.some((f) => f.path === expanded)) return;
    readDiff(expanded);
  }, [expanded, diffs, listed, readDiff]);

  // ---- find in diff ----
  const [findOpen, setFindOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [current, setCurrent] = useState<string | null>(null);
  const findInput = useRef<HTMLInputElement>(null);
  const q = findOpen ? query.trim() : "";
  const finding = q.length >= MIN_QUERY;
  // The server's answer, kept with the list and query it is for: an answer to
  // anything else is stale and reads as none.
  const [found, setFound] = useState<{
    changes: ThreadChanges;
    query: string;
    paths: Set<string>;
    failed: boolean;
  } | null>(null);
  const answer = found && found.changes === changes && found.query === q ? found : null;
  const serverPaths = answer?.paths ?? null;
  const searching = finding && !serverPaths;
  const findRef = useLatest(findInDiff);

  useEffect(() => {
    if (!finding || !changes) return;
    const list = changes;
    let live = true;
    const timer = setTimeout(() => {
      const land = (paths: string[], failed: boolean) =>
        live && setFound({ changes: list, query: q, paths: new Set(paths), failed });
      // A failed search still settles, the path-name matches standing alone,
      // but says so: it is not the same answer as "nothing in the diff".
      findRef.current(q, list).then(
        (paths) => land(paths, false),
        () => land([], true),
      );
    }, FIND_DELAY);
    return () => {
      live = false;
      clearTimeout(timer);
    };
  }, [finding, q, changes, findRef]);

  const matches = useMemo(() => {
    if (!finding) return listed;
    const needle = q.toLowerCase();
    return listed.filter((f) => f.path.toLowerCase().includes(needle) || serverPaths?.has(f.path));
  }, [finding, q, listed, serverPaths]);
  const at = current === null ? -1 : matches.findIndex((f) => f.path === current);

  // ---- scrolling to what was asked for, once it is on screen ----
  const listRef = useRef<HTMLDivElement>(null);
  const rowRefs = useRef(new Map<string, HTMLDivElement>());
  const scrollRow = useRef<{ path: string; smooth: boolean } | null>(null);
  const scrollMatch = useRef<string | null>(null);

  useEffect(() => {
    const row = scrollRow.current;
    const el = row && rowRefs.current.get(row.path);
    if (row && el) {
      el.scrollIntoView({ block: "start", behavior: row.smooth ? "smooth" : "auto" });
      scrollRow.current = null;
    }
    const path = scrollMatch.current;
    if (!path) return;
    if (expanded !== path) {
      scrollMatch.current = null;
      return;
    }
    const entry = diffs[path];
    if (!entry || entry.loading) return;
    rowRefs.current.get(path)?.querySelector("[data-diff-match]")?.scrollIntoView({ block: "center" });
    scrollMatch.current = null;
  });

  const step = (by: 1 | -1) => {
    if (!matches.length) return;
    const next = at === -1 ? (by === 1 ? 0 : matches.length - 1) : (at + by + matches.length) % matches.length;
    const path = matches[next].path;
    setCurrent(path);
    setExpanded(path);
    setRestore(null);
    scrollRow.current = { path, smooth: false };
    scrollMatch.current = path;
  };

  const openFind = (open: boolean) => {
    setFindOpen(open);
    if (!open) {
      setQuery("");
      setCurrent(null);
    }
  };
  useEffect(() => {
    if (findOpen) findInput.current?.focus();
  }, [findOpen]);

  // ---- rows ----
  const toggle = (path: string) => {
    setExpanded((cur) => (cur === path ? null : path));
    setRestore(null);
    if (finding) setCurrent(path);
    // Expanding a row twice does not re-read it; a failed read is retried.
    if (diffs[path]?.error) {
      asked.current?.paths.delete(path);
      setRead((r) => {
        const rest = { ...r.diffs };
        delete rest[path];
        return { ...r, diffs: rest };
      });
    }
  };

  // Reveal whatever the transcript asked for, once the list it belongs to has
  // arrived. A path the list does not carry is not an error: the file may have
  // been written since the list was read, so the list is read once more, or
  // changed by an earlier turn and put back since, so a list read after the
  // request that still lacks it drops the request. Left waiting, it would
  // keep a remount from restoring its place and fire on some later list.
  const revealHandledRef = useLatest(onRevealHandled);
  const refreshRef = useLatest(onRefresh);
  // The nonce whose list is being re-read, and whether that read has started.
  const revealRead = useRef<{ nonce: number; started: boolean } | null>(null);
  useEffect(() => {
    if (!reveal) return;
    if (files.some((f) => f.path === reveal.path)) {
      revealRead.current = null;
      setExpanded(reveal.path);
      setRevealed(reveal.path);
      setRestore(null);
      scrollRow.current = { path: reveal.path, smooth: true };
      revealHandledRef.current?.(reveal.nonce);
      return;
    }
    const read = revealRead.current?.nonce === reveal.nonce ? revealRead.current : null;
    if (loading) {
      if (read) read.started = true;
      else revealRead.current = { nonce: reveal.nonce, started: true };
      return;
    }
    if (!changes && !error) return;
    if (read?.started) {
      revealRead.current = null;
      revealHandledRef.current?.(reveal.nonce);
    } else if (!read) {
      revealRead.current = { nonce: reveal.nonce, started: false };
      refreshRef.current();
    }
    // The nonce is what makes a repeat click count as a new request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reveal?.path, reveal?.nonce, files, loading, changes, error]);

  // Put the remembered scroll back once the list can hold it, or once nothing
  // more is coming that would make it taller.
  const openEntry = expanded ? diffs[expanded] : undefined;
  const settled =
    !!error ||
    (!!changes &&
      !loading &&
      (!expanded || !listed.some((f) => f.path === expanded) || (!!openEntry && !openEntry.loading)));
  useLayoutEffect(() => {
    const el = listRef.current;
    if (restore === null || !el) return;
    if (el.scrollHeight - el.clientHeight < restore && !settled) return;
    el.scrollTop = restore;
    setRestore(null);
  });

  const onScroll = () => {
    // A list still filling in scrolls on its own; that is not where it was left.
    if (restore !== null || !listRef.current) return;
    rememberDiffPlace(placeKey, { scrollTop: listRef.current.scrollTop });
  };

  // ---- the toolbar ----
  const toggles: DiffToggle[] = [
    ...(desktop ? [{ label: "Split view", Icon: Columns2Icon, on: split, set: setSplit }] : []),
    // Split view always wraps (see Diff), so the toggle would do nothing there.
    ...(desktop && split ? [] : [{ label: "Wrap text", Icon: WrapTextIcon, on: wrap, set: setWrap }]),
    { label: "Ignore whitespace", Icon: PilcrowIcon, on: ignoreWhitespace, set: setIgnoreWhitespace },
    { label: "Show full files", Icon: UnfoldVerticalIcon, on: fullContext, set: setFullContext },
    ...(testCount > 0
      ? [
          {
            label: "Hide test files",
            Icon: FlaskConicalOffIcon,
            on: hideTests,
            set: (hide: boolean) => {
              // Asking to hide tests hides them all, the revealed one included.
              if (hide) setRevealed(null);
              setHideTests(hide);
            },
          },
        ]
      : []),
  ];

  const noun =
    comparison === "turn"
      ? "turn"
      : comparison === "commit"
        ? commits && commits.from !== commits.to
          ? "range of commits"
          : "commit"
        : "thread";
  const nothing =
    comparison === "turn"
      ? "This turn changed nothing."
      : comparison === "commit"
        ? commits && commits.from !== commits.to
          ? "These commits changed nothing."
          : "This commit changed nothing."
        : "Nothing changed yet.";

  const shown = finding ? matches : listed;

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* Wraps rather than overflows: docked narrow, the options drop to a
          second line under the scope. */}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 border-b px-3 py-1.5">
        <div className="flex min-w-0 items-center gap-2">
          <DiffScopeSelect
            comparison={comparison}
            turn={turn}
            pr={pr}
            commits={commits}
            onComparisonChange={onComparisonChange}
            loadCommits={loadCommits}
          />
          <span className="text-muted-foreground shrink-0 text-[11px] whitespace-nowrap">
            {finding ? `${matches.length} of ${plural(listed.length, "file")}` : plural(listed.length, "file")}
            {hiddenCount > 0 && (
              <span title={`${plural(hiddenCount, "test file")} hidden`}>
                {" · "}
                <span className="sm:hidden">{hiddenCount} hidden</span>
                <span className="hidden sm:inline">{plural(hiddenCount, "test file")} hidden</span>
              </span>
            )}
          </span>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-1">
          {totals && <Counts additions={totals.additions} deletions={totals.deletions} />}
          <DiffToolbar desktop={desktop} toggles={toggles} findOpen={findOpen} onFind={openFind} menuFocus={findInput} />
          <IconButton label="Re-read the worktree" onClick={onRefresh}>
            <RefreshCwIcon className={cn(loading && "spin-icon")} />
          </IconButton>
        </div>
      </div>

      {findOpen && (
        <DiffFindBar
          inputRef={findInput}
          query={query}
          onQuery={(next) => {
            setQuery(next);
            setCurrent(null);
          }}
          current={at}
          total={finding ? matches.length : null}
          searching={searching}
          failed={!!answer?.failed}
          onStep={step}
          onClose={() => openFind(false)}
        />
      )}

      <div
        ref={listRef}
        onScroll={onScroll}
        className="scroll-thin min-h-0 flex-1 overflow-y-auto overscroll-contain"
      >
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
            {loading && !changes ? "Reading the worktree…" : nothing}
          </p>
        )}
        {!error && files.length > 0 && listed.length === 0 && (
          <p className="text-muted-foreground px-3 py-10 text-center text-[12px]">
            Only test files changed, and they are hidden.
          </p>
        )}
        {!error && finding && listed.length > 0 && matches.length === 0 && (
          <p className="text-muted-foreground px-3 py-10 text-center text-[12px]">
            {searching ? "Searching…" : `No file matches “${q}”.`}
          </p>
        )}
        {shown.map((f) => (
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
            layout={layout}
            highlight={finding ? q : undefined}
            onToggle={() => toggle(f.path)}
            onAddComment={onAddComment}
          />
        ))}
        {changes?.truncated && (
          <p className="text-muted-foreground px-3 py-2 text-[11px] italic">
            Only the first files are listed; this {noun} changed more than the panel will show.
          </p>
        )}
      </div>
    </div>
  );
}
