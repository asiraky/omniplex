import { useCallback, useEffect, useRef, useState } from "react";

import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import type { BranchCommit, BranchCommits, DiffComparison, DiffTurn, PullRequest } from "~/protocol";
import { useLatest } from "~/useLatest";

/** A `commit` scope: the oldest and newest commit it includes. */
export interface CommitRange {
  from: string;
  to: string;
}

type CommitList = { loading: boolean; data?: BranchCommits; error?: string };

const COMMIT = "commit:";
// Tall enough for a thumb on a phone, and a long subject truncates rather
// than pushing the menu off the screen.
const ITEM = "min-h-11 md:min-h-0 *:[span]:last:min-w-0";
const sha7 = (sha: string) => sha.slice(0, 7);

/**
 * The branch's commits, read when the select opens (and on a mount that is
 * already showing commits, which needs them for the range picker) — never on
 * a render. A re-read keeps the last list on screen while it runs.
 */
function useCommitList(load: () => Promise<BranchCommits>, wantedAtMount: boolean) {
  const [list, setList] = useState<CommitList>({ loading: false });
  const generation = useRef(0);
  const loadRef = useLatest(load);

  const fetch = useCallback(() => {
    const g = ++generation.current;
    setList((l) => ({ ...l, loading: true, error: undefined }));
    loadRef.current().then(
      (data) => g === generation.current && setList({ loading: false, data }),
      (e) =>
        g === generation.current &&
        setList((l) => ({ ...l, loading: false, error: e instanceof Error ? e.message : String(e) })),
    );
  }, [loadRef]);

  useEffect(() => {
    if (wantedAtMount) fetch();
    // Mount only: what is wanted later is asked for by opening the select.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return [list, fetch] as const;
}

function CommitText({ commit }: { commit: BranchCommit }) {
  return (
    <>
      <span className="text-muted-foreground shrink-0 font-mono text-[11px]">{sha7(commit.sha)}</span>
      <span className="min-w-0 truncate">{commit.subject}</span>
    </>
  );
}

/**
 * What the diff surface compares against: uncommitted, the branch, the PR, a
 * turn a card opened — or one of the branch's commits, extended into a range
 * by a second, compact select beside it.
 */
export function DiffScopeSelect({
  comparison,
  turn,
  pr,
  commits,
  onComparisonChange,
  loadCommits,
}: {
  comparison: DiffComparison;
  turn?: DiffTurn | null;
  pr?: PullRequest | null;
  commits?: CommitRange | null;
  onComparisonChange: (comparison: DiffComparison, commits?: CommitRange) => void;
  loadCommits: () => Promise<BranchCommits>;
}) {
  const range = comparison === "commit" && commits ? commits : null;
  const [list, fetchCommits] = useCommitList(loadCommits, !!range);
  const all = list.data?.commits ?? [];

  const value = range ? COMMIT + range.from : comparison;
  const choose = (v: string) => {
    if (v.startsWith(COMMIT)) {
      const sha = v.slice(COMMIT.length);
      onComparisonChange("commit", { from: sha, to: sha });
    } else {
      onComparisonChange(v as DiffComparison);
    }
  };

  // Extending runs from the chosen commit towards the newest, which the list
  // holds first.
  const fromAt = range ? all.findIndex((c) => c.sha === range.from) : -1;
  const through = fromAt === -1 ? [] : all.slice(0, fromAt + 1);
  const toAt = range ? through.findIndex((c) => c.sha === range.to) : -1;
  const rangeCount = toAt === -1 ? null : fromAt - toAt + 1;

  return (
    <>
      <Select value={value} onValueChange={choose} onOpenChange={(open) => open && fetchCommits()}>
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
            {comparison === "turn" && (turn ? `Turn ${turn.number}` : "Turn")}
            {comparison === "commit" && (
              <span className="font-mono">
                {commits
                  ? commits.from === commits.to
                    ? sha7(commits.from)
                    : `${sha7(commits.from)}…${sha7(commits.to)}`
                  : "Commits"}
              </span>
            )}
          </SelectValue>
        </SelectTrigger>
        <SelectContent className="max-w-[min(26rem,calc(100vw-2rem))]">
          <SelectItem value="uncommitted">Uncommitted changes</SelectItem>
          <SelectItem value="branch">Branch changes</SelectItem>
          {(pr || comparison === "pull_request") && (
            <SelectItem value="pull_request">
              {pr ? `PR #${pr.number} vs ${pr.baseRefName || "base"}` : "Attached PR"}
            </SelectItem>
          )}
          {turn && <SelectItem value="turn">Turn {turn.number}</SelectItem>}
          <SelectSeparator />
          <SelectGroup>
            <SelectLabel>{list.data?.baseRef ? `Commits since ${list.data.baseRef}` : "Commits"}</SelectLabel>
            {all.map((c) => (
              <SelectItem key={c.sha} value={COMMIT + c.sha} className={ITEM}>
                <CommitText commit={c} />
              </SelectItem>
            ))}
            {/* The commit on screen keeps an item even when the list no longer
                carries it, so the select still has something to show as chosen. */}
            {range && fromAt === -1 && !list.loading && (
              <SelectItem value={COMMIT + range.from} disabled>
                <span className="font-mono text-[11px]">{sha7(range.from)}</span>
                <span>not on this branch now</span>
              </SelectItem>
            )}
            {list.loading && all.length === 0 && (
              <SelectItem value="_commits-loading" disabled>
                Reading commits…
              </SelectItem>
            )}
            {list.error && (
              <SelectItem value="_commits-error" disabled className="text-destructive">
                {list.error}
              </SelectItem>
            )}
            {list.data && !list.error && all.length === 0 && (
              <SelectItem value="_commits-none" disabled>
                {list.data.warning || "No commits on this branch yet."}
              </SelectItem>
            )}
            {list.data?.truncated && (
              <SelectItem value="_commits-more" disabled>
                Only the latest {all.length} commits are listed.
              </SelectItem>
            )}
          </SelectGroup>
        </SelectContent>
      </Select>

      {range && (
        <Select
          value={range.to}
          onValueChange={(to) => onComparisonChange("commit", { from: range.from, to })}
          onOpenChange={(open) => open && !list.data && fetchCommits()}
        >
          <SelectTrigger aria-label="Extend to a range of commits" className="h-8 min-w-0 shrink-0 text-[12px]">
            <SelectValue>
              {range.from === range.to ? "1 commit" : rangeCount ? `${rangeCount} commits` : "Range"}
            </SelectValue>
          </SelectTrigger>
          <SelectContent className="max-w-[min(26rem,calc(100vw-2rem))]">
            {through.map((c) => (
              <SelectItem key={c.sha} value={c.sha} className={ITEM}>
                {c.sha === range.from ? (
                  "Only this commit"
                ) : (
                  <>
                    <span className="shrink-0">…through</span>
                    <CommitText commit={c} />
                  </>
                )}
              </SelectItem>
            ))}
            {through.length === 0 && (
              <SelectItem value={range.from}>
                Only this commit
              </SelectItem>
            )}
            {range.to !== range.from && !through.some((c) => c.sha === range.to) && (
              <SelectItem value={range.to} disabled>
                …through <span className="font-mono text-[11px]">{sha7(range.to)}</span>
              </SelectItem>
            )}
            {list.loading && !list.data && (
              <SelectItem value="_range-loading" disabled>
                Reading commits…
              </SelectItem>
            )}
          </SelectContent>
        </Select>
      )}
    </>
  );
}
